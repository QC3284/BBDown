package util

import (
	"testing"
)

// 新版字幕接口是 protobuf，字段号取自 BBDownT 的 dmviewreply.proto：
// SubtitleWebReply{subtitle=1} / VideoSubtitle{subtitles=3} / SubtitleItem{lan=3, lanDoc=4, subtitleUrl=5}。
//
// 变异验证：把解码器里的字段号改错（如 lan 用 2）→ 本用例变红。
func TestParseSubtitleWebReplyDecodesProtobuf(t *testing.T) {
	item := append(protoEncodeBytes(3, []byte("zh-CN")), protoEncodeBytes(4, []byte("中文（简体）"))...)
	item = append(item, protoEncodeBytes(5, []byte("//aisubtitle.example.com/a.json"))...)

	videoSubtitle := protoEncodeBytes(3, item) // repeated subtitles
	reply := protoEncodeBytes(1, videoSubtitle)

	subs := parseSubtitleWebReply(reply)
	if len(subs) != 1 {
		t.Fatalf("应当解出 1 条字幕，实际 %d（%+v）", len(subs), subs)
	}
	if subs[0].Lan != "zh-CN" {
		t.Errorf("lan = %q，期望 zh-CN", subs[0].Lan)
	}
	if subs[0].URL != "https://aisubtitle.example.com/a.json" {
		t.Errorf("协议相对地址应当补成 https:，实际 %q", subs[0].URL)
	}

	// 多条 subtitle 都要解出来；缺 lan 或 url 的条目跳过（不产出半条字幕）。
	item2 := protoEncodeBytes(3, []byte("ai-zh"))
	reply2 := protoEncodeBytes(1, append(protoEncodeBytes(3, item), protoEncodeBytes(3, item2)...))
	if subs := parseSubtitleWebReply(reply2); len(subs) != 1 {
		t.Errorf("缺 url 的条目应当被跳过，实际 %+v", subs)
	}

	// 空 / 垃圾数据不能 panic，只能返回空。
	if subs := parseSubtitleWebReply(nil); subs != nil {
		t.Errorf("空响应应当返回 nil，实际 %+v", subs)
	}
	if subs := parseSubtitleWebReply([]byte{0xff, 0xff, 0xff}); subs != nil {
		t.Errorf("损坏数据应当返回 nil，实际 %+v", subs)
	}
}

// protoEncodeBytes 构造一个 length-delimited 字段（仅测试用）。
func protoEncodeBytes(number int, payload []byte) []byte {
	out := protoEncodeVarint(uint64(number)<<3 | 2)
	out = append(out, protoEncodeVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

// protoEncodeVarint 构造一个 varint（仅测试用）。
func protoEncodeVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}
