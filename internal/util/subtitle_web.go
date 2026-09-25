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
const subtitleWebAPI = "https://api.bilibili.com/x/v2/subtitle/web/view"

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
		subs = append(subs, entity.Subtitle{Lan: lan, URL: normalizeSubtitleURL(subURL)})
	}
	return subs
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
