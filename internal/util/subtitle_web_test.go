package util

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestSubtitleWebFixtureDecodesObfuscatedURL 是 t44 的核心断言：真机响应里那条「乱码」
// subtitle_url 是**百分号编码后的 XOR 密文**，按 BBDownT 7408653 的算法解出来就是真实的
// aisubtitle.hdslb.com 地址（逐字，含 auth_key）。
//
// 真机夹具的期望值 = 先手工跑一遍解码得到的（见 t44 报告），与接口当日返回的 auth_key 绑定。
// 变异验证：
//   - 撤掉 XOR（直接用密文当路径）→ 本用例红（不会得到 /bfs/ai_subtitle/prod/…）；
//   - 撤掉宿主重写（仍然返回占位宿主）→ 本用例红。
func TestSubtitleWebFixtureDecodesObfuscatedURL(t *testing.T) {
	const wantURL = "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/117161691712819412918093111bade14ecbdad26d698352bd58f9c70e?auth_key=1791295684-afb444f5d06549f1a5e436ed7f236319-0-18bee78ace65fe1757005bdd61676c27"

	subs := parseSubtitleWebReply(subtitleWebFixture(t))
	if len(subs) != 1 {
		t.Fatalf("真机夹具应当解出 1 条字幕，实际 %+v", subs)
	}
	if subs[0].Lan != "ai-zh" {
		t.Errorf("lan = %q, want ai-zh", subs[0].Lan)
	}
	if subs[0].URL != wantURL {
		t.Errorf("解码后的字幕地址 = %q\n                    want %q", subs[0].URL, wantURL)
	}
	if !strings.HasPrefix(subs[0].URL, "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/") {
		t.Errorf("解出来的应当是真实 AI 字幕路径：%q", subs[0].URL)
	}
	if strings.Contains(subs[0].URL, subtitleWebObfuscatedHost) {
		t.Errorf("占位宿主必须被重写掉：%q", subs[0].URL)
	}
	// auth_key 原样保留（逐字比对 query 段）。
	if got, want := subs[0].URL[strings.Index(subs[0].URL, "?"):], wantURL[strings.Index(wantURL, "?"):]; got != want {
		t.Errorf("query 段被改动了：got %q, want %q", got, want)
	}

	// 解不开的形态（两对常量都匹配不上）仍然判为不可用 → 调用方按无字幕回退。
	rawURL := "//subtitle.bilibili.com/S%13%1BP.%1D%28%29X?auth_key=1-2-0-3"
	if _, ok := decodeSubtitleObfuscatedURL(rawURL); ok {
		t.Errorf("这对常量匹配不上，不该解码成功：%q", rawURL)
	}
	if usableSubtitleURL(rawURL) {
		t.Errorf("占位宿主 %s 的地址必须判为不可用（回退路径）：%q", subtitleWebObfuscatedHost, rawURL)
	}
}

// TestDecodeSubtitleObfuscatedURLDegrades 钉住「形态不符就静默降级」：一律 ok=false、绝不 panic，
// 让调用方走老接口回退，而不是把一条假地址交给下载器。
func TestDecodeSubtitleObfuscatedURLDegrades(t *testing.T) {
	cases := map[string]string{
		"空串":             "",
		"只有空白":           "   ",
		"不是 URL":         "not-a-url",
		"别的宿主（真 CDN 地址）": "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/x.srt?auth_key=1",
		"占位宿主但编码非法":      "//subtitle.bilibili.com/S%1%2?auth_key=1",
		"占位宿主但密文匹配不上":    "//subtitle.bilibili.com/plaintext?auth_key=1",
		"占位宿主但解出的路径不含白名单前缀": "//subtitle.bilibili.com/%01%02%03?auth_key=1",
	}
	for name, in := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s：解码不该 panic，实际 panic=%v", name, r)
				}
			}()
			if got, ok := decodeSubtitleObfuscatedURL(in); ok {
				t.Errorf("%s：应当解码失败，实际 (%q, true)", name, got)
			}
		}()
	}
}

// TestValidDecodedSubtitlePath 钉住路径白名单（BBDownT 的「阻止二次解码」那几条）：
// 前缀白名单 + 拒绝 '%' / '?' / '#' / '\\' / 控制字符 / '.' '..' 段。
func TestValidDecodedSubtitlePath(t *testing.T) {
	ok := []string{
		"/bfs/ai_subtitle/prod/1234567890",
		"/bfs/ai_subtitle/prod/a/b/c",
		"/bfs/subtitle/2026/whatever.json",
	}
	bad := []string{
		"",
		"/bfs/ai_subtitle/prod/",
		"/bfs/subtitle/",
		"/bfs/other/x",
		"/bfs/ai_subtitle/prod/a%2Fb", // '%' 会让后续解析二次解码
		"/bfs/ai_subtitle/prod/a?b",
		"/bfs/ai_subtitle/prod/a#b",
		"/bfs/ai_subtitle/prod/a\\b",
		"/bfs/ai_subtitle/prod/../etc",
		"/bfs/ai_subtitle/prod/./a",
		"/bfs/ai_subtitle/prod/a\nb",
	}
	for _, p := range ok {
		if !validDecodedSubtitlePath(p) {
			t.Errorf("应当合法：%q", p)
		}
	}
	for _, p := range bad {
		if validDecodedSubtitlePath(p) {
			t.Errorf("应当拒绝：%q", p)
		}
	}
}

// TestSubtitleObfuscationConstantsProvenance 钉住两对常量的长度与首字符，防止以后误改：
// 出处 = /tmp/BBDownT/BBDownT.Core/Util/SubtitleUrlResolver.cs:8-14（Encodings）。
func TestSubtitleObfuscationConstantsProvenance(t *testing.T) {
	if len(subtitleObfuscationEncodings) != 2 {
		t.Fatalf("应当有两对 prefix/key，实际 %d", len(subtitleObfuscationEncodings))
	}
	wantLen := [][2]int{{32, 40}, {33, 42}}
	for i, enc := range subtitleObfuscationEncodings {
		if len(enc.Prefix) != wantLen[i][0] || len(enc.Key) != wantLen[i][1] {
			t.Errorf("第 %d 对常量长度变了：prefix=%d key=%d，want %v", i+1, len(enc.Prefix), len(enc.Key), wantLen[i])
		}
		if !strings.HasSuffix(enc.Key, "bilibili") {
			t.Errorf("第 %d 对的 key 应当以 bilibili 收尾：%q", i+1, enc.Key)
		}
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

// TestGetSubWebAPIDecodesAndPreparesPath 是端到端的离线用例（假端点 + 真夹具）：
// 解码出真实地址之外，还要**补上落盘路径与目录**——真机实测过两个坑：
//
//	① 路径为空 → 「open : no such file or directory」；
//	② 目录不存在（--sub-only 时没有媒体/封面阶段去建 <aid>/）→ open <aid>/… 失败。
//
// 变异验证：把 URL 换成占位宿主（撤宿主重写）或撤掉 XOR → 断言红。
func TestGetSubWebAPIDecodesAndPreparesPath(t *testing.T) {
	t.Chdir(t.TempDir())
	fixture := subtitleWebFixture(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	restore := subtitleWebEndpoint
	subtitleWebEndpoint = srv.URL
	defer func() { subtitleWebEndpoint = restore }()

	client := NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	subs := getSubWebAPI(context.Background(), client, "117161691712819", "41291809311")
	if len(subs) != 1 {
		t.Fatalf("应当解出 1 条字幕，实际 %+v", subs)
	}
	const wantPath = "117161691712819/117161691712819.41291809311.ai-zh.srt"
	if subs[0].Path != wantPath {
		t.Errorf("字幕落盘路径 = %q, want %q", subs[0].Path, wantPath)
	}
	if _, err := os.Stat(filepath.Dir(subs[0].Path)); err != nil {
		t.Errorf("字幕目录应当被建出来（--sub-only 时没有别的阶段建它）：%v", err)
	}
	if !strings.HasPrefix(subs[0].URL, "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/") {
		t.Errorf("URL 应当是解码后的真实地址：%q", subs[0].URL)
	}
}

// TestGetSubtitlesTriesWebAPIWithoutCookie 钉住 2.15.3 的门控放宽：新版字幕接口**不再要求 cookie**
// （t44 证明匿名也能拿 AI 字幕；t50 审查证伪了 t36 时代「匿名拿不到」的门控理由）。
//
// 变异验证：把 subtitle.go 里 getSubWebAPI 的调用恢复成 cookie != "" 门控 → 本用例红
// （web 端点调用数 = 0 且 subs 为空；门控下匿名会走 API3——该路径对 AI 字幕视频返回空列表）。
func TestGetSubtitlesTriesWebAPIWithoutCookie(t *testing.T) {
	t.Chdir(t.TempDir())
	fixture := subtitleWebFixture(t)
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	restore := subtitleWebEndpoint
	subtitleWebEndpoint = srv.URL
	defer func() { subtitleWebEndpoint = restore }()

	client := NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	// cookie 传空串：门控放宽后仍应命中新版接口。
	subs, err := GetSubtitles(context.Background(), client, "117161691712819", "41291809311", "", 0, false, "")
	if err != nil {
		t.Fatalf("GetSubtitles 出错：%v", err)
	}
	if calls != 1 {
		t.Fatalf("空 cookie 也应调用新版字幕接口一次（实际 %d 次）", calls)
	}
	if len(subs) != 1 || !strings.HasPrefix(subs[0].URL, "https://aisubtitle.hdslb.com/bfs/ai_subtitle/prod/") {
		t.Fatalf("匿名应解出 AI 字幕真实地址：%+v", subs)
	}
}
