package util

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
)

// 新版字幕接口（x/v2/subtitle/web/view）——返回 **protobuf** 而不是 JSON。
//
// 字段号来自上游同生态 C# 2.x 接手线 BBDownT 的 BBDownT.Core/APP/Response/dmviewreply.proto：
//
//	message SubtitleWebReply { optional VideoSubtitle subtitle = 1; }
//	message VideoSubtitle    { optional string lan = 1; optional string lanDoc = 2; repeated SubtitleItem subtitles = 3; }
//	message SubtitleItem     { int64 id = 1; string idStr = 2; string lan = 3; string lanDoc = 4; string subtitleUrl = 5; ... }
//
// 只手解需要的三个字段（lan/lanDoc/subtitleUrl），因此不必引入 protobuf 运行时与生成代码。
//
// # t36 真机取证：逐字段全量解码（夹具 = subtitle_web_test.go 的 subtitleWebFixtureHex）
//
// 匿名抓一次 BV1B58d6PEXz 的响应（405 字节），用 protowire 逐条解出来是：
//
//	顶层           field 1, wire 2, len 402  → 内层（= SubtitleWebReply.subtitle，即 VideoSubtitle）
//	VideoSubtitle  field 3, wire 2, len 399  → 条目（= subtitles，这里只有一条）
//	SubtitleItem   field 1, varint        = 2094374763291239936    → id（int64）
//	               field 2, wire 2, len 19 = "2094374763291239936"  → idStr
//	               field 3, wire 2, len 5  = "ai-zh"                → lan
//	               field 4, wire 2, len 6  = "中文"                  → lanDoc
//	               field 5, wire 2, len 336= "//subtitle.bilibili.com/S%13%1BP…?auth_key=…" → subtitleUrl
//	               field 7 varint = 1；field 8 string = "中文"；field 9 varint = 1；field 10 varint = 2
//
// 结论一（**没有字段漂移**）：我们要的三个字段在 web 响应里就是 3/4/5，与 BBDownT 的 app proto 一致
// （只有 id/idStr 在 web 侧是 1/2、app 侧是 2/1 的互换，以及外层包裹位置不同——app 放
// DmViewReply.subtitle=3，web 放 SubtitleWebReply.subtitle=1；本文件按 web 的 1 号位解析）。
// 长度也读对了：field 5 的 336 字节被完整消费，紧随其后的 field 7..10 都能正常解析。
//
// 结论二（**subtitleUrl 是服务端不可还原的混淆令牌**，见 usableSubtitleURL）：字段内容本身是
// 纯 ASCII 的合法 URL 串，但 ——
//   - 宿主 subtitle.bilibili.com 在公网**不存在**：AliDNS 的 JSON API（带/不带中国 ECS）与
//     Cloudflare DoH 都返回 NXDOMAIN（authority 是 bilibili.com 的 NS3），而真正的 AI 字幕 CDN
//     aisubtitle.hdslb.com 正常解析（多个 CDN IP）；
//   - 宿主与 ?auth_key= 之间的路径是一段 **7bit 二进制块**（全部字节 < 0x80，含 %00/%0A/%13 这类
//     控制字节），同一内容重复请求时逐字节相同、内容变了就变——不是字段错位、也不是长度读错，
//     是服务端对真实路径做了加密/混淆（由 B 站自己的 web 客户端解密）。
//   - 反推算法已试并排除：拿响应上下文（auth_key、id/idStr、lan、oid/pid、宿主、"bilibili"）
//     做循环 XOR / 加减 / 位旋转 / 半字节交换，以及单字节穷举，都得不出可读路径。
//     所以这个地址**不能当成可下载地址**交给下载器：原样下载只会得到一条 DNS 失败——这正是
//     「字幕地址疑似失效」的根因（零产物 + 侧车误报 ✔，见 workflow 的 subtitleSidecarState）。
const subtitleWebAPI = "https://api.bilibili.com/x/v2/subtitle/web/view"

// subtitleWebObfuscatedHost 是新版接口那把**混淆令牌**的宿主：它不存在于公网 DNS（见上），
// 出现它就意味着这条地址不可用。判据只针对这个已知宿主，不做「猜哪些 CDN 可用」的启发式。
const subtitleWebObfuscatedHost = "subtitle.bilibili.com"

// getSubWebAPI 请求新版字幕接口并解码；失败返回 nil（由调用方回退到老接口）。
func getSubWebAPI(ctx context.Context, client *HTTPClient, aid, cid string) []entity.Subtitle {
	contextExt, _ := json.Marshal(map[string]int{"video_type": 1})
	api := fmt.Sprintf("%s?oid=%s&pid=%s&context_ext=%s&type=1&cur_production_type=0&preferred_language=ai-zh&playlist_switch=0",
		subtitleWebAPI, url.QueryEscape(cid), url.QueryEscape(aid), url.QueryEscape(string(contextExt)))

	body, err := client.GetWebSource(ctx, api)
	if err != nil {
		LogDebug("新版字幕接口失败: %v", err)
		return nil
	}
	return parseSubtitleWebReply([]byte(body))
}

// parseSubtitleWebReply 从 protobuf 响应里解出字幕列表。
func parseSubtitleWebReply(data []byte) []entity.Subtitle {
	videoSubtitle := protoBytesField(data, 1) // SubtitleWebReply.subtitle
	if videoSubtitle == nil {
		return nil
	}
	var subs []entity.Subtitle
	for _, item := range protoRepeatedBytes(videoSubtitle, 3) { // VideoSubtitle.subtitles
		lan := string(protoBytesField(item, 3))    // SubtitleItem.lan
		subURL := string(protoBytesField(item, 5)) // SubtitleItem.subtitleUrl
		if lan == "" || subURL == "" {
			continue
		}
		// 服务端混淆令牌（宿主不存在于公网 DNS）不是可下载地址：**丢弃它**而不是原样返回。
		// 返回 nil 会让 GetSubtitles 继续走老三条接口（登录态下 player/wbi/v2 给的是可用的
		// aisubtitle.hdslb.com 地址）——否则一条不可用地址会挡住全部回退，用户看到的就是
		// 「零产物 + 任务完成」。
		if !usableSubtitleURL(subURL) {
			LogDebug("新版字幕接口返回了不可用的混淆地址（宿主 %s），跳过该条并按无字幕回退", subtitleWebObfuscatedHost)
			continue
		}
		subs = append(subs, entity.Subtitle{Lan: lan, URL: normalizeSubtitleURL(subURL)})
	}
	return subs
}

// usableSubtitleURL 判断接口给的字幕地址是不是**真的能下**。
//
// 只排除有确凿证据的不可用形态（不猜 CDN 白名单）：
//   - 空 / 解析失败 / 非 http(s)（协议相对地址先按 normalizeSubtitleURL 补全）；
//   - 宿主为空；
//   - 宿主是新版接口那把混淆令牌的占位宿主 subtitle.bilibili.com——公网 NXDOMAIN（见文件头取证），
//     路径是服务端加密的 7bit 块，客户端无法还原。
func usableSubtitleURL(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	u, err := url.Parse(normalizeSubtitleURL(raw))
	if err != nil || u.Hostname() == "" {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	return !strings.EqualFold(u.Hostname(), subtitleWebObfuscatedHost)
}

// normalizeSubtitleURL 补全协议相对地址（接口常给 //aisubtitle.hdslb.com/...）。
func normalizeSubtitleURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

// protowireField 描述一个 protobuf 字段（只需支持我们关心的两类：varint 与 length-delimited）。
type protowireField struct {
	Number int
	Bytes  []byte // length-delimited 的内容
}

// protoFields 顺序遍历 protobuf 消息，返回所有 length-delimited 字段。
// 解析失败（截断/非法 wire type）即停止——宁少勿错，剩下的交给回退路径。
func protoFields(data []byte) []protowireField {
	var out []protowireField
	for len(data) > 0 {
		key, n := protoVarint(data)
		if n <= 0 {
			return out
		}
		data = data[n:]
		number := int(key >> 3)
		switch key & 7 {
		case 0: // varint
			_, m := protoVarint(data)
			if m <= 0 {
				return out
			}
			data = data[m:]
		case 2: // length-delimited
			l, m := protoVarint(data)
			if m <= 0 || int(l) > len(data)-m {
				return out
			}
			out = append(out, protowireField{Number: number, Bytes: data[m : m+int(l)]})
			data = data[m+int(l):]
		case 5: // 32-bit
			if len(data) < 4 {
				return out
			}
			data = data[4:]
		case 1: // 64-bit
			if len(data) < 8 {
				return out
			}
			data = data[8:]
		default:
			return out // 不认识的 wire type：放弃解析，交给回退路径
		}
	}
	return out
}

// protoBytesField 返回第一个指定字段号的 length-delimited 内容。
func protoBytesField(data []byte, number int) []byte {
	for _, f := range protoFields(data) {
		if f.Number == number {
			return f.Bytes
		}
	}
	return nil
}

// protoRepeatedBytes 返回所有指定字段号的 length-delimited 内容（repeated 字段）。
func protoRepeatedBytes(data []byte, number int) [][]byte {
	var out [][]byte
	for _, f := range protoFields(data) {
		if f.Number == number {
			out = append(out, f.Bytes)
		}
	}
	return out
}

// protoVarint 读一个 varint，返回 (值, 消耗字节数)；n <= 0 表示数据不完整。
func protoVarint(data []byte) (uint64, int) {
	var value uint64
	var shift uint
	for i, b := range data {
		if i >= 10 {
			return 0, -1
		}
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, i + 1
		}
		shift += 7
	}
	return 0, -1
}
