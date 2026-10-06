package util

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/QC3284/bbdown-go/internal/entity"
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

// subtitleWebEndpoint 是请求用的端点；变量而非常量：离线用例注入假服务器（同 internal/download
// 里那些「给测试留的接缝」的做法），生产路径就是 subtitleWebAPI。
var subtitleWebEndpoint = subtitleWebAPI

// subtitleWebObfuscatedHost 是新版接口那把**混淆令牌**的宿主：它不存在于公网 DNS（见上），
// 出现它就意味着这条地址不可用。判据只针对这个已知宿主，不做「猜哪些 CDN 可用」的启发式。
const subtitleWebObfuscatedHost = "subtitle.bilibili.com"

// getSubWebAPI 请求新版字幕接口并解码；失败返回 nil（由调用方回退到老接口）。
func getSubWebAPI(ctx context.Context, client *HTTPClient, aid, cid string) []entity.Subtitle {
	contextExt, _ := json.Marshal(map[string]int{"video_type": 1})
	api := fmt.Sprintf("%s?oid=%s&pid=%s&context_ext=%s&type=1&cur_production_type=0&preferred_language=ai-zh&playlist_switch=0",
		subtitleWebEndpoint, url.QueryEscape(cid), url.QueryEscape(aid), url.QueryEscape(string(contextExt)))

	body, err := client.GetWebSource(ctx, api)
	if err != nil {
		LogDebug("新版字幕接口失败: %v", err)
		return nil
	}
	subs := parseSubtitleWebReply([]byte(body))
	// 新版接口的条目只带 lan/URL，**没有落盘路径**：这里按老三条接口的同一格式补上
	// （<aid>/<aid>.<cid>.<lan>.srt）——下载层按 s.Path 落盘，缺了它会拿空路径去 open
	// （真机实测：字幕下载失败: open : no such file or directory）。
	for i := range subs {
		if subs[i].Path == "" {
			subs[i].Path = fmt.Sprintf("%s/%s.%s.%s.srt", aid, aid, cid, SanitizePathSegment(subs[i].Lan))
		}
		// 下载层按 s.Path 直接 open，不会建目录：老三条接口时代的 <aid>/ 目录是**媒体/封面阶段**
		// 顺手建出来的，而 --sub-only 没有那一步（真机实测：字幕下载失败: open <aid>/…: no such
		// file or directory）。这里补上目录，让「仅字幕」也能落盘；失败不致命——保存时会报真实错误。
		if dir := filepath.Dir(subs[i].Path); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0o755)
		}
	}
	return subs
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
		// t44：这个 path 是「百分号编码后的 XOR 密文」——先按 BBDownT 的算法解成真实地址
		// （见 decodeSubtitleObfuscatedURL）。解出来就是可下载的 aisubtitle.hdslb.com 地址。
		if decoded, ok := decodeSubtitleObfuscatedURL(subURL); ok {
			subs = append(subs, entity.Subtitle{Lan: lan, URL: decoded})
			continue
		}
		// 解不开（形态不符/未知编码）：退回 t36 的可用性过滤——不可用就**丢弃它**而不是原样
		// 返回（返回 nil 会让 GetSubtitles 继续走老三条接口，登录态下 player/wbi/v2 给的是
		// 可用的 aisubtitle.hdslb.com 地址），否则一条不可用地址会挡住全部回退。
		if !usableSubtitleURL(subURL) {
			LogDebug("新版字幕接口返回了无法解码的地址（宿主 %s），跳过该条并按无字幕回退", subtitleWebObfuscatedHost)
			continue
		}
		subs = append(subs, entity.Subtitle{Lan: lan, URL: normalizeSubtitleURL(subURL)})
	}
	return subs
}

// ---- t44：新版接口 subtitle_url 的双重混淆解码（吸收 BBDownT commit 7408653）----
//
// 真相：新版接口那条「乱码」URL 的 path 是**百分号编码后的 XOR 密文**：
//  1. 百分号解码（Uri.UnescapeDataString / url.PathUnescape）拿到密文字节；
//  2. 逐字节与重复 key 做 XOR（key[i % len(key)]）；
//  3. 明文以固定 prefix 开头，剥掉 prefix 得到真实路径（/bfs/ai_subtitle/prod/… 或 /bfs/subtitle/…）；
//  4. 宿主重写成 https://aisubtitle.hdslb.com，query（auth_key）原样保留。
//
// 解出来的路径还会**拒绝 '%'** 等字符：否则后续 URI 解析会把它再解一次（这正是 BBDownT
// 那个 commit 标题里的「阻止二次解码」）。
//
// 出处（逐字移植，勿凭记忆重打）：/tmp/BBDownT/BBDownT.Core/Util/SubtitleUrlResolver.cs
//   - 两对 (Prefix, Key) 常量：该文件 9-13 行（Encodings，第 9 行起）
//   - 算法 Normalize：该文件 15-56 行；其中百分号合法性校验 26-31 行、
//     Uri.UnescapeDataString 33 行、逐字节 XOR 38 行、明文 prefix 判定 41 行、
//     路径白名单（含拒绝 '%'）43-51 行、宿主重写 + 保留 query 54 行
//
// 常量本身的具体值与两对顺序由 TestSubtitleObfuscationConstantsProvenance 与
// TestSubtitleWebFixtureDecodesObfuscatedURL 钉住（前者长度/尾串，后者逐字 URL）。
//
// 常量本身是 B 站播放器公开常量（注释里给出播放器 JS 出处，非账号凭据）；
// 两对 prefix/key 至今都由 web 接口返回，所以两对都要试。
var subtitleObfuscationEncodings = []struct{ Prefix, Key string }{
	{"nP](wOFRvU.+<fjS{jn-!$D|Dz&\",zT`", "=CFxYRn{.y|uVyO$uh&sikph?N.ilF/`bilibili"},
	{"Bn\"q~|albg@]Go~ACgyDvKnd+)_D}^&J?", "Cu~L!xs~f^&r@'vh=q]q{eeng*sEg^kp#Jbilibili"},
}

// subtitleDecodedHost 是解码后真实字幕 CDN 的宿主（与 BBDownT 一致）。
const subtitleDecodedHost = "https://aisubtitle.hdslb.com"

// decodeSubtitleObfuscatedURL 把新版接口的混淆 subtitle_url 解成可下载的真实地址。
// 形态不符（宿主不是占位宿主 / 百分号编码非法 / 两对常量都匹配不上 / 解出的路径不合法）时返回 ok=false——
// 调用方按「解不开」处理（回退老接口），绝不 panic。
func decodeSubtitleObfuscatedURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(normalizeSubtitleURL(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	// 只处理占位宿主：别的宿主（真实 CDN 地址）不是密文，交给调用方按可用性判断处理。
	if !strings.EqualFold(u.Hostname(), subtitleWebObfuscatedHost) {
		return "", false
	}
	// 用 EscapedPath 拿**未解码**的 path（u.Path 已经解码过一次了），并去掉前导 '/'：
	// 密文从占位宿主后面第一个字符开始（C# 的 uri.AbsolutePath[1..] 同义）。
	encodedPath := strings.TrimPrefix(u.EscapedPath(), "/")
	for i := 0; i < len(encodedPath); i++ {
		if encodedPath[i] != '%' {
			continue
		}
		if i+2 >= len(encodedPath) || !isHexDigit(encodedPath[i+1]) || !isHexDigit(encodedPath[i+2]) {
			return "", false
		}
		i += 2
	}
	cipher, err := url.PathUnescape(encodedPath)
	if err != nil {
		return "", false
	}
	query := ""
	if u.RawQuery != "" {
		query = "?" + u.RawQuery
	}
	for _, enc := range subtitleObfuscationEncodings {
		if len(enc.Key) == 0 {
			continue
		}
		plain := make([]byte, len(cipher))
		for i := range cipher {
			plain[i] = cipher[i] ^ enc.Key[i%len(enc.Key)]
		}
		text := string(plain)
		if !strings.HasPrefix(text, enc.Prefix) {
			continue
		}
		path := text[len(enc.Prefix):]
		if !validDecodedSubtitlePath(path) {
			return "", false
		}
		return subtitleDecodedHost + path + query, true
	}
	return "", false
}

// validDecodedSubtitlePath 校验解出来的路径（与 BBDownT 的白名单逐条对应）：
//   - 必须是 /bfs/subtitle/<非空> 或 /bfs/ai_subtitle/prod/<非空>；
//   - 不含控制字符，也不含 '%'、'?'、'#'、'\'——'%' 尤其重要：留着它，后面的 URI 解析会
//     把路径**再解一次**（BBDownT 7408653 的「阻止二次解码」就是这条）；
//   - 不含 '.' / '..' 段（与 2.12.x 的占位符/路径穿越防护一致）。
func validDecodedSubtitlePath(path string) bool {
	const (
		subPrefix   = "/bfs/subtitle/"
		aiSubPrefix = "/bfs/ai_subtitle/prod/"
	)
	okPrefix := (strings.HasPrefix(path, subPrefix) && len(path) > len(subPrefix)) ||
		(strings.HasPrefix(path, aiSubPrefix) && len(path) > len(aiSubPrefix))
	if !okPrefix {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7F {
			return false
		}
		switch r {
		case '%', '?', '#', '\\':
			return false
		}
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// isHexDigit 与 C# 的 Uri.IsHexDigit 同义（只认 0-9A-Fa-f）。
func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
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
