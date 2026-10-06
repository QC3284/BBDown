package util

import (
	"encoding/hex"
	"strings"
	"testing"
)

// t36 夹具：**真实**的匿名响应（405 字节，BV1B58d6PEXz），抓取命令：
//
//	curl -H 'Accept: application/octet-stream' \
//	  'https://api.bilibili.com/x/v2/subtitle/web/view?oid=41291809311&pid=117161691712819&context_ext=%7B%22video_type%22%3A1%7D&type=1&cur_production_type=0&preferred_language=ai-zh&playlist_switch=0'
//
// 逐字段解码结论写在 subtitle_web.go 的文件头（字段号/类型 + 为什么 subtitleUrl 不可用）。
const subtitleWebFixtureHex = "0a92031a8f030880cc918483d8ad881d1213323039343337343736333239313233393933361a0561692d7a682206e4b8ade696872ad0022f2f7375627469746c652e62696c6962696c692e636f6d2f53253133253142502e25314425323825323958253243522535456a2531462532357725304525303248253545484f34253134253742342530384b402533432537422530304d2530422530412531414d253038253035364e3624253043302625303225314525303125303925304525314132567e2531354259253130425f52415f253045253743253136585572253145522535425854592535425a25354458253043253231253237253143253343635a2531454d253142253138253134324b794043512531454025354359253132253043253041764850253046712531462530353f617574685f6b65793d313739313239353638342d61666234343466356430363534396631613565343336656437663233363331392d302d313862656537386163653635666531373537303035626464363136373663323738014206e4b8ade6968748015002"

func subtitleWebFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(subtitleWebFixtureHex, " ", ""))
	if err != nil {
		t.Fatalf("夹具 hex 解不开: %v", err)
	}
	if len(b) != 405 {
		t.Fatalf("夹具长度 = %d, want 405（换夹具时必须同步更新 subtitle_web.go 文件头的解码结论）", len(b))
	}
	return b
}

// ---- 手写 protobuf 编码（用例侧构造响应，避免为了造数据引入运行时/生成代码）----

func encVarint(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func encBytesField(field int, b []byte) []byte {
	out := encVarint(uint64(field)<<3 | 2)
	out = append(out, encVarint(uint64(len(b)))...)
	return append(out, b...)
}

func encVarintField(field int, v uint64) []byte {
	return append(encVarint(uint64(field)<<3|0), encVarint(v)...)
}

// subtitleWebReplyBytes 造一份与真机同构的响应：顶层 field1 = VideoSubtitle，其 field3 = 条目。
func subtitleWebReplyBytes(lan, lanDoc, subtitleURL string) []byte {
	item := append(encVarintField(1, 2094374763291239936), encBytesField(2, []byte("2094374763291239936"))...)
	item = append(item, encBytesField(3, []byte(lan))...)
	item = append(item, encBytesField(4, []byte(lanDoc))...)
	item = append(item, encBytesField(5, []byte(subtitleURL))...)
	return encBytesField(1, encBytesField(3, item))
}

// TestSubtitleWebFixtureFieldNumbers 钉住真机响应的字段号/类型（t36 ①）：
// VideoSubtitle 在顶层 field 1、条目在它的 field 3；条目里 lan=3（字符串）、lanDoc=4（字符串）、
// subtitleUrl=5（字符串），id=1 是 varint（int64）、idStr=2 是字符串——与 BBDownT 的 app proto
// 相比只有 id/idStr 互换、外层包裹位置不同，我们要的三个字段没有漂移。
//
// 变异验证：把 parseSubtitleWebReply 里读 subtitleUrl 的字段号从 5 改成 4 → 本用例红。
func TestSubtitleWebFixtureFieldNumbers(t *testing.T) {
	data := subtitleWebFixture(t)

	videoSubtitle := protoBytesField(data, 1)
	if videoSubtitle == nil {
		t.Fatal("顶层 field 1 应当是一个内层消息（VideoSubtitle）")
	}
	items := protoRepeatedBytes(videoSubtitle, 3)
	if len(items) != 1 {
		t.Fatalf("VideoSubtitle 里应当有 1 条字幕，实际 %d", len(items))
	}
	item := items[0]

	if got := string(protoBytesField(item, 3)); got != "ai-zh" {
		t.Errorf("条目 field 3（lan) = %q, want ai-zh", got)
	}
	if got := string(protoBytesField(item, 4)); got != "中文" {
		t.Errorf("条目 field 4（lanDoc）= %q, want 中文", got)
	}
	rawURL := string(protoBytesField(item, 5))
	if !strings.HasPrefix(rawURL, "//subtitle.bilibili.com/") || !strings.Contains(rawURL, "?auth_key=") {
		t.Fatalf("条目 field 5（subtitleUrl）形态不对：%q", rawURL)
	}
	if len(rawURL) != 336 {
		t.Errorf("subtitleUrl 长度 = %d, want 336（长度读错会让后面的字段解析错位）", len(rawURL))
	}
	// id 是 varint（web 侧 1 号位）、idStr 是字符串（2 号位）：与 app proto 的 2/1 相反。
	if got := string(protoBytesField(item, 2)); got != "2094374763291239936" {
		t.Errorf("条目 field 2（idStr）= %q", got)
	}
	if !strings.HasSuffix(rawURL, "18bee78ace65fe1757005bdd61676c27") {
		t.Errorf("auth_key 尾段（明文）没读出来：%q", rawURL[len(rawURL)-40:])
	}
}

// TestSubtitleWebFixtureObfuscatedURLRejected 是本次修复的核心断言（t36 ③）：
// 真机响应里唯一那条字幕的地址是**服务端混淆令牌**（宿主公网 NXDOMAIN、路径是 7bit 密文），
// 解析必须丢弃它并返回 nil——让调用方回退到老三条接口，而不是把不可用地址交给下载器。
//
// 变异验证：撤掉 usableSubtitleURL 的判断 → 本用例红（会返回 1 条不可用地址）。
func TestSubtitleWebFixtureObfuscatedURLRejected(t *testing.T) {
	subs := parseSubtitleWebReply(subtitleWebFixture(t))
	if subs != nil {
		t.Fatalf("混淆令牌不该通过解析，实际 %+v", subs)
	}
	rawURL := "//subtitle.bilibili.com/S%13%1BP.%1D%28%29X?auth_key=1-2-0-3"
	if usableSubtitleURL(rawURL) {
		t.Errorf("占位宿主 %s 的地址必须判为不可用：%q", subtitleWebObfuscatedHost, rawURL)
	}
}

// TestParseSubtitleWebReplyAcceptsUsableURL 反向钉住过滤器不是「一律拒绝」：
// 老接口给的真 CDN 地址（aisubtitle.hdslb.com）必须原样通过，并补全协议。
func TestParseSubtitleWebReplyAcceptsUsableURL(t *testing.T) {
	body := subtitleWebReplyBytes("zh-CN", "中文（简体）", "//aisubtitle.hdslb.com/bfs/ai_subtitle/prod/x.srt?auth_key=1-2-0-3")
	subs := parseSubtitleWebReply(body)
	if len(subs) != 1 {
		t.Fatalf("可用地址应当解析出 1 条，实际 %+v", subs)
	}
	if subs[0].Lan != "zh-CN" {
		t.Errorf("lan = %q, want zh-CN", subs[0].Lan)
	}
	if subs[0].URL != "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/x.srt?auth_key=1-2-0-3" {
		t.Errorf("URL = %q（协议相对地址应当补成 https）", subs[0].URL)
	}
}

// TestUsableSubtitleURL 表驱动钉住判据：空白/无宿主/非 http(s)/占位宿主 → 不可用；真 CDN → 可用。
func TestUsableSubtitleURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"真机那把混淆令牌", "//subtitle.bilibili.com/S%13%1BP.x?auth_key=1", false},
		{"占位宿主的 https 形态", "https://subtitle.bilibili.com/a.srt", false},
		{"占位宿主大小写不同", "https://SUBTitle.bilibili.com/a.srt", false},
		{"真实 AI 字幕 CDN（协议相对）", "//aisubtitle.hdslb.com/bfs/ai_subtitle/prod/a.srt?auth_key=1", true},
		{"真实 AI 字幕 CDN（https）", "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/a.srt", true},
		{"人工字幕 CDN", "https://i0.hdslb.com/bfs/subtitle/a.json", true},
		{"空串", "", false},
		{"只有空白", "   ", false},
		{"没有宿主", "a.srt", false},
		{"非 http(s)", "ftp://aisubtitle.hdslb.com/a.srt", false},
	}
	for _, c := range cases {
		if got := usableSubtitleURL(c.raw); got != c.want {
			t.Errorf("%s：usableSubtitleURL(%q) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
}

// TestParseSubtitleWebReplyDegradesSilently 保持既有纪律：解析失败一律返回 nil、绝不 panic
// （字幕是装饰性资源，接口抖一下不能把下载带崩）。
func TestParseSubtitleWebReplyDegradesSilently(t *testing.T) {
	ok := subtitleWebReplyBytes("ai-zh", "中文", "//aisubtitle.hdslb.com/x.srt")
	cases := map[string][]byte{
		"空响应":        {},
		"只有一个字节":     {0x0a},
		"长度超出数据":     {0x0a, 0x7f, 0x01, 0x02},
		"未知 wire 类型": {0x0f, 0x01},
		"字段乱序/垃圾":    append([]byte{0xff, 0xff, 0xff, 0xff}, ok...),
		"截断的合法响应":    ok[:len(ok)/2],
		"没有字幕的合法响应":  subtitleWebReplyBytes("", "", ""),
	}
	for name, data := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s：解析不该 panic，实际 panic=%v", name, r)
				}
			}()
			subs := parseSubtitleWebReply(data)
			if len(subs) != 0 {
				t.Errorf("%s：应当返回空，实际 %+v", name, subs)
			}
		}()
	}
}
