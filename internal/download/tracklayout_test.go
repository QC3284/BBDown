package download

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// stripANSI 去掉输出里的颜色码：内容通道在 TTY 下给每行套 AnsiCyan/AnsiReset。
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= 'a' && s[j] <= 'z') && !(s[j] >= 'A' && s[j] <= 'Z') {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// layoutLines 取出捕获输出里的流清单**数据行**：过 ANSI、去掉内容行的一个空格缩进。
// 只留含码率的行——段标题（可用流（6））与表头都不含 "kbps"。
func layoutLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(stripANSI(out), "\n") {
		l = strings.TrimLeft(l, " ")
		if strings.Contains(l, "kbps") {
			lines = append(lines, l)
		}
	}
	return lines
}

// findLayoutLine 按内容挑出唯一一行，挑不到或有歧义就结束用例（避免断言在空切片上假绿）。
func findLayoutLine(t *testing.T, lines []string, marker string) string {
	t.Helper()
	var got []string
	for _, l := range lines {
		if strings.Contains(l, marker) {
			got = append(got, l)
		}
	}
	if len(got) != 1 {
		t.Fatalf("含 %q 的清单行应恰好 1 条，实际 %d 条：%q", marker, len(got), lines)
	}
	return got[0]
}

// displayColumn 返回 marker 起始处的显示列（marker 之前那段文本占的列数）。
func displayColumn(t *testing.T, line, marker string) int {
	t.Helper()
	i := strings.Index(line, marker)
	if i < 0 {
		t.Fatalf("行里没有 %q：%q", marker, line)
	}
	return DisplayWidth(line[:i])
}

// displayColumnEnd 返回 marker 结束处的显示列。
func displayColumnEnd(t *testing.T, line, marker string) int {
	t.Helper()
	return displayColumn(t, line, marker) + DisplayWidth(marker)
}

// TestDisplayWidth 是排版的地基：中文/全角算 2 列，空串 0 列，ASCII 每字符 1 列。
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"1080P 高清", 10},  // 5 + 空格 1 + 2×2
		{"8K 超高清", 9},     // 2 + 1 + 3×2
		{"1080P 高帧率", 12}, // QualityMap 里最长的清晰度名
		{"未知(30000)", 11}, // 未收录档位的兜底名：未知 4 + 5 位数字 + 括号
		{"[视频]", 6},       // 选中轨道的标签（旧版行首标记，宽度 6）
		{"ＡＢ", 4},         // 全角字母
		{"。，！", 6},        // 中文标点（全角）
		{"\u200b", 0},     // 零宽空格
		{"e\u0301", 1},    // e + 组合重音
		{"🎬", 2},          // emoji
	}
	for _, c := range cases {
		if got := DisplayWidth(c.in); got != c.want {
			t.Errorf("DisplayWidth(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}

	// 陷阱本身：两个标签 rune 数相同、显示宽度不同。fmt 的 "%-Ns" 按 rune 补空格，
	// 会认为这两者一样宽——这正是旧排版歪掉的原因。
	if DisplayWidth("高清") == DisplayWidth("AB") {
		t.Errorf("rune 数相同的标签显示宽度必须不同：高清=%d, AB=%d", DisplayWidth("高清"), DisplayWidth("AB"))
	}
}

// TestPadDisplay 覆盖按显示宽度补空格的三条边界：空串、恰好、超宽（不截断）。
func TestPadDisplay(t *testing.T) {
	right := []struct {
		in    string
		width int
		want  string
	}{
		{"", 4, "    "},
		{"abc", 5, "abc  "},
		{"中文", 6, "中文  "},
		{"恰好", 4, "恰好"},
		{"超宽内容不截断", 2, "超宽内容不截断"},
	}
	for _, c := range right {
		got := PadDisplay(c.in, c.width)
		if got != c.want {
			t.Errorf("PadDisplay(%q, %d) = %q，期望 %q", c.in, c.width, got, c.want)
		}
		if w := DisplayWidth(got); w < c.width || w < DisplayWidth(c.in) {
			t.Errorf("PadDisplay(%q, %d) = %q 只有 %d 列", c.in, c.width, got, w)
		}
	}
	left := []struct {
		in    string
		width int
		want  string
	}{
		{"30", 4, "  30"},
		{"12345", 3, "12345"},
		{"", 2, "  "},
		{"中文", 6, "  中文"},
	}
	for _, c := range left {
		if got := padDisplayLeft(c.in, c.width); got != c.want {
			t.Errorf("padDisplayLeft(%q, %d) = %q，期望 %q", c.in, c.width, got, c.want)
		}
	}
}

// TestSectionTitleAlignsByDisplayWidth 是标题行的核心判据：中文标题与 ASCII 标题补出来的
// 标题行显示宽度必须一样（用 len()/rune 数补白时，中文标题那行会短一截）。
//
// 变异验证：SectionTitle 里的 DisplayWidth 改成 len(head) → 本用例红。
func TestSectionTitleAlignsByDisplayWidth(t *testing.T) {
	cjk := SectionTitle("字幕君交流场所")
	ascii := SectionTitle("abc")
	t.Logf("中文：%q（%d 列）", cjk, DisplayWidth(cjk))
	t.Logf("ASCII：%q（%d 列）", ascii, DisplayWidth(ascii))

	if w := DisplayWidth(cjk); w != SectionWidth {
		t.Errorf("中文标题行应补到 %d 列，实际 %d 列：%q", SectionWidth, w, cjk)
	}
	if w := DisplayWidth(ascii); w != SectionWidth {
		t.Errorf("ASCII 标题行应补到 %d 列，实际 %d 列：%q", SectionWidth, w, ascii)
	}
	if !strings.HasPrefix(cjk, "── 字幕君交流场所 ") || !strings.HasSuffix(cjk, "─") {
		t.Errorf("标题行形态不符（应为 ── 标题 + 补齐的横线）：%q", cjk)
	}
	// 超宽标题不截断（宁可这一行长一点，也不吃掉标题里的字）。
	long := SectionTitle(strings.Repeat("宽", SectionWidth))
	if !strings.HasSuffix(long, "宽") || DisplayWidth(long) < SectionWidth {
		t.Errorf("超宽标题不该被截断或补白：%q", long)
	}
}

// TestTrackTableMatchesTargetLayout 钉住定稿的列宽与两行样例（目标形态的原文）：
// 序号不带点、帧率两位小数、体积不带 "~"、表头与数据行同列。
//
// 变异验证：任一列宽改动（如 trackKbpsWidth 8 → 10）→ 逐字节断言红。
func TestTrackTableMatchesTargetLayout(t *testing.T) {
	av1 := entity.Video{Dfn: "480P 清晰", Res: "512x384", Codecs: "AV1", FPS: "15.009", Bandwidth: 206, Dur: 2055}
	avc := entity.Video{Dfn: "480P 清晰", Res: "512x384", Codecs: "AVC", FPS: "14.925", Bandwidth: 155, Dur: 2055}
	cases := []struct {
		index int
		v     entity.Video
		want  string
	}{
		{0, av1, " 0  480P 清晰     512x384    AV1   15.01  206 kbps  51.68 MB"},
		{1, avc, " 1  480P 清晰     512x384    AVC   14.93  155 kbps  38.88 MB"},
	}
	for _, c := range cases {
		if got := formatVideoTrackLine(c.index, c.v, 2055); got != c.want {
			t.Errorf("第 %d 行排版不符：\n得到 %q\n期望 %q", c.index, got, c.want)
		}
	}

	const wantHeader = " #  清晰度        分辨率     编码   帧率    码率      体积"
	if videoTrackHeader != wantHeader {
		t.Errorf("表头排版不符：\n得到 %q\n期望 %q", videoTrackHeader, wantHeader)
	}
}

// TestAudioHeaderColumnsMatchAudioRows 音频段表头与数据行同列：音频没有分辨率/编码/帧率三列，
// 但码率与体积仍落在与视频段相同的显示列上（表头的空列必须占位，否则整段左移）。
func TestAudioHeaderColumnsMatchAudioRows(t *testing.T) {
	videoRow := formatVideoTrackLine(0, entity.Video{Dfn: "480P 清晰", Res: "512x384", Codecs: "AV1", FPS: "15.009", Bandwidth: 206, Dur: 2055}, 2055)
	audioRow := formatAudioTrackLine(0, entity.Audio{Codecs: "M4A", Bandwidth: 134, Dur: 2055}, 2055)

	// 数据列：音频行的码率/体积与视频行同列（右对齐列的右边缘一致）。
	if got, want := displayColumnEnd(t, audioRow, "134 kbps"), displayColumnEnd(t, videoRow, "206 kbps"); got != want {
		t.Errorf("音频行码率列右边缘在第 %d 列，视频行在第 %d 列：%q / %q", got, want, audioRow, videoRow)
	}
	if got, want := displayColumnEnd(t, audioRow, "33.61 MB"), displayColumnEnd(t, videoRow, "51.68 MB"); got != want {
		t.Errorf("音频行体积列右边缘在第 %d 列，视频行在第 %d 列：%q / %q", got, want, audioRow, videoRow)
	}

	// 表头：两段的标签居中落在同一列（数值列居中，文本列左对齐）。
	if got, want := displayColumn(t, audioTrackHeader, "码率"), displayColumn(t, videoTrackHeader, "码率"); got != want {
		t.Errorf("视频段与音频段的码率标签不在同一列：%d vs %d", got, want)
	}
	if got, want := displayColumn(t, audioTrackHeader, "体积"), displayColumn(t, videoTrackHeader, "体积"); got != want {
		t.Errorf("视频段与音频段的体积标签不在同一列：%d vs %d", got, want)
	}
	// 标签必须落在它标注的那一列里（居中不等于漂到隔壁列去）。
	colStart := displayColumn(t, videoRow, "206 kbps")
	colEnd := displayColumnEnd(t, videoRow, "206 kbps")
	if s, e := displayColumn(t, audioTrackHeader, "码率"), displayColumnEnd(t, audioTrackHeader, "码率"); s < colStart || e > colEnd {
		t.Errorf("码率标签（%d~%d 列）应当落在码率列（%d~%d 列）之内：%q", s, e, colStart, colEnd, audioTrackHeader)
	}
}

// TestTrackLinesAlignByDisplayWidth 是这次排版的核心判据：标签显示宽度不同的两行，
// 整行显示宽度必须一致，且清晰度列、右对齐的码率/体积列都落在同一显示列。
//
// 两个夹具的标签 rune 数相同（"高清" / "AB"）而显示宽度差 2 列，码率也跨过列宽（3000 kbps
// 比 8 列宽 1 列）——只要补空格不是按 DisplayWidth 算、或右对齐列超宽时不回收左侧填充，
// 下面任意一条断言都会红。
func TestTrackLinesAlignByDisplayWidth(t *testing.T) {
	narrow := entity.Video{Dfn: "高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 1, Dur: 100}
	wide := entity.Video{Dfn: "AB", Res: "854x480", Codecs: "HEVC", FPS: "60", Bandwidth: 3000, Dur: 100}
	lineA := formatVideoTrackLine(0, narrow, 100)
	lineB := formatVideoTrackLine(1, wide, 100)
	t.Logf("A（%d 列）%s", DisplayWidth(lineA), lineA)
	t.Logf("B（%d 列）%s", DisplayWidth(lineB), lineB)

	if wA, wB := DisplayWidth(lineA), DisplayWidth(lineB); wA != wB {
		t.Errorf("两行显示宽度不一致（%d vs %d），列没对齐：\n%s\n%s", wA, wB, lineA, lineB)
	}
	if cA, cB := displayColumn(t, lineA, "高清"), displayColumn(t, lineB, "AB"); cA != cB {
		t.Errorf("清晰度列不在同一显示列：%d vs %d\n%s\n%s", cA, cB, lineA, lineB)
	}
	if cA, cB := displayColumnEnd(t, lineA, "kbps"), displayColumnEnd(t, lineB, "kbps"); cA != cB {
		t.Errorf("码率列右边缘不在同一显示列：%d vs %d\n%s\n%s", cA, cB, lineA, lineB)
	}
	if cA, cB := displayColumnEnd(t, lineA, "12.50 KB"), displayColumnEnd(t, lineB, "36.62 MB"); cA != cB {
		t.Errorf("体积列右边缘不在同一显示列：%d vs %d\n%s\n%s", cA, cB, lineA, lineB)
	}

	// 空列（没有分辨率/帧率的轨道）同样不塌陷、不甩出方括号兜底
	bare := formatVideoTrackLine(2, entity.Video{Dfn: "1080P 高清", Codecs: "AVC", Bandwidth: 3000, Dur: 100}, 100)
	if wBare, wA := DisplayWidth(bare), DisplayWidth(lineA); wBare != wA {
		t.Errorf("空列行宽度 %d 与其它行 %d 不一致：%q", wBare, wA, bare)
	}
	if strings.ContainsAny(bare, "[]") {
		t.Errorf("空列不该用方括号占位：%q", bare)
	}

	// 极端码率（20000 kbps，比列宽长 2 列）：允许整行变宽，但列间至少留一个空格，
	// 不许把两个单元贴死（回收填充的下限，见 trackLine.right）。
	huge := formatVideoTrackLine(3, entity.Video{Dfn: "8K", Res: "7680x4320", Codecs: "AV1", FPS: "120", Bandwidth: 20000, Dur: 100}, 100)
	if !strings.Contains(huge, " 20000 kbps 244.14 MB") {
		t.Errorf("超宽码率与体积之间至少留一个空格：%q", huge)
	}
}

// TestSelectedLinesShareTheListLayout 选中的两条流与清单共用同一套列宽：行首是选中标记 "*"
// （宽度与序号列一致），名称列、码率列、体积列都落在与清单行相同的显示列上。
//
// 变异验证：把选中行的前缀改回 [视频]/[音频]（6 列塞进 1 列前缀列）→ 列位置错开，本用例红。
func TestSelectedLinesShareTheListLayout(t *testing.T) {
	video := entity.Video{ID: "80", Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100, BaseURL: "https://cdn/v.m4s"}
	audio := entity.Audio{ID: "30280", Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 100, BaseURL: "https://cdn/a.m4s"}

	listOut := captureStdout(t, func() {
		PrintAllTracks(&entity.ParsedResult{VideoTracks: []entity.Video{video}, AudioTracks: []entity.Audio{audio}}, 100, false)
	})
	selOut := captureStdout(t, func() { PrintSelectedTrack(&video, &audio, 100) })
	listLines := layoutLines(listOut)
	selLines := layoutLines(selOut)

	lines := []struct {
		name string
		line string
	}{
		{"清单·视频", findLayoutLine(t, listLines, "1920x1080")},
		{"清单·音频", findLayoutLine(t, listLines, "mp4a.40.2")},
		{"选中·视频", findLayoutLine(t, selLines, "1920x1080")},
		{"选中·音频", findLayoutLine(t, selLines, "mp4a.40.2")},
	}
	wantKbpsEnd := displayColumnEnd(t, lines[0].line, "kbps")
	wantNameCol := displayColumn(t, lines[0].line, "1080P 高清")
	for _, l := range lines {
		if got := displayColumnEnd(t, l.line, "kbps"); got != wantKbpsEnd {
			t.Errorf("%s 行码率列右边缘在第 %d 列，与清单·视频行第 %d 列不一致：%q", l.name, got, wantKbpsEnd, l.line)
		}
	}
	// 体积列：音频行与视频行的体积文本不同，分别按各自的行取右边缘比对。
	if got, want := displayColumnEnd(t, lines[0].line, "36.62 MB"), displayColumnEnd(t, lines[2].line, "36.62 MB"); got != want {
		t.Errorf("选中视频的体积列右边缘在第 %d 列，清单在第 %d 列：%q", want, got, lines[2].line)
	}
	if got, want := displayColumnEnd(t, lines[1].line, "1.61 MB"), displayColumnEnd(t, lines[3].line, "1.61 MB"); got != want {
		t.Errorf("选中音频的体积列右边缘在第 %d 列，清单在第 %d 列：%q", want, got, lines[3].line)
	}
	for _, i := range []int{1, 2, 3} {
		if got := displayColumn(t, lines[i].line, map[int]string{1: "mp4a.40.2", 2: "1080P 高清", 3: "mp4a.40.2"}[i]); got != wantNameCol {
			t.Errorf("%s 的名称列在第 %d 列，清单·视频在第 %d 列：%q", lines[i].name, got, wantNameCol, lines[i].line)
		}
	}
	for _, i := range []int{2, 3} {
		if !strings.HasPrefix(lines[i].line, "*") {
			t.Errorf("%s 行应以选中标记 * 开头：%q", lines[i].name, lines[i].line)
		}
	}

	// 段标题里写明是视频还是音频（行首那个 6 列的 [视频] 标签塞不进 1 列的序号列）。
	for _, want := range []string{"── 已选择的视频流", "── 已选择的音频流"} {
		if !strings.Contains(selOut, want) {
			t.Errorf("选中段缺少标题 %q：%q", want, selOut)
		}
	}
}

// TestTrackLinesCarryEveryField 信息一条不减：清晰度/分辨率/编码/帧率/码率/体积仍在行里，
// 只是方括号换成了列间距、序号不带点、体积不再带 "~"（目标形态的定稿）。
func TestTrackLinesCarryEveryField(t *testing.T) {
	video := entity.Video{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100}
	line := formatVideoTrackLine(2, video, 100)
	for _, want := range []string{"1080P 高清", "1920x1080", "AVC", "30.00", "3000 kbps", "36.62 MB"} {
		if !strings.Contains(line, want) {
			t.Errorf("视频行缺少 %q：%q", want, line)
		}
	}
	if !strings.HasPrefix(line, " 2  ") {
		t.Errorf("序号列应是 2 加两个空格（不再带点）：%q", line)
	}
	if strings.ContainsAny(line, "[]") {
		t.Errorf("视频清单行不该再有方括号：%q", line)
	}

	audio := entity.Audio{Codecs: "M4A", Bandwidth: 192, Dur: 100}
	aline := formatAudioTrackLine(0, audio, 100)
	for _, want := range []string{"M4A", "192 kbps", "2.34 MB"} {
		if !strings.Contains(aline, want) {
			t.Errorf("音频行缺少 %q：%q", want, aline)
		}
	}
	if !strings.HasPrefix(aline, " 0  ") {
		t.Errorf("音频序号列应是 0 加两个空格：%q", aline)
	}
	if strings.ContainsAny(aline, "[]") {
		t.Errorf("音频清单行不该再有方括号：%q", aline)
	}
}

// TestTrackLineDurationFallback 体积的取值规则与改前一致：分P时长优先、缺失时用轨道时长；
// 接口给了 size 就不再估算。只有 "~" 前缀按目标形态去掉了。
func TestTrackLineDurationFallback(t *testing.T) {
	v := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60}
	if line := formatVideoTrackLine(0, v, 0); !strings.Contains(line, "750.00 KB") { // 60×100kbps×1024/8 = 768000
		t.Errorf("分P时长缺失时应按轨道时长估算体积：%q", line)
	}
	if line := formatVideoTrackLine(0, v, 120); !strings.Contains(line, "1.46 MB") { // 120×100×1024/8 = 1536000
		t.Errorf("分P时长存在时应优先使用：%q", line)
	}
	withSize := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60, Size: 2048}
	if line := formatVideoTrackLine(0, withSize, 0); !strings.Contains(line, "2.00 KB") {
		t.Errorf("接口给出 size 时不该再估算：%q", line)
	}
	a := entity.Audio{Codecs: "M4A", Bandwidth: 192, Dur: 100}
	if line := formatAudioTrackLine(0, a, 0); !strings.Contains(line, "2.34 MB") {
		t.Errorf("音频分P时长缺失时应按轨道时长估算体积：%q", line)
	}
}

// TestFormatTrackFPSRoundsDecimally 钉住帧率的两位小数口径：playurl 有的端点给 "30"、
// 有的给 "15.009"；14.925 按十进制四舍五入是 14.93——用 float 的 %.2f 会得到 14.92
// （14.925 的 float64 存储值略小于它），这正是这条用例存在的理由。
//
// 变异验证：formatTrackFPS 改成 strconv.FormatFloat(f, 'f', 2) → 14.93 那条变红。
func TestFormatTrackFPSRoundsDecimally(t *testing.T) {
	cases := []struct{ in, want string }{
		{"15.009", "15.01"},
		{"14.925", "14.93"},
		{"30", "30.00"},
		{"15.5", "15.50"},
		{"0.999", "1.00"},
		{"", ""},
		{"30000/1001", "30000/1001"}, // 非数值原样保留
		{" 60 ", "60.00"},
	}
	for _, c := range cases {
		if got := formatTrackFPS(c.in); got != c.want {
			t.Errorf("formatTrackFPS(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestFormatDurationShort 钉住信息行里的短时长：不足一小时是 MM:SS，超过一小时是 H:MM:SS
// （util.FormatTime(absolute=true) 的 "00:34:15" 在信息行里那个 "00:" 是噪音）。
func TestFormatDurationShort(t *testing.T) {
	cases := []struct {
		sec  int
		want string
	}{
		{2055, "34:15"},
		{59, "0:59"},
		{60, "1:00"},
		{3600, "1:00:00"},
		{3933, "1:05:33"},
		{0, "0:00"},
	}
	for _, c := range cases {
		if got := FormatDurationShort(c.sec); got != c.want {
			t.Errorf("FormatDurationShort(%d) = %q，期望 %q", c.sec, got, c.want)
		}
	}
}

// TestElideURL 钉住终端里的直链截断规则：查询串整段丢掉（保留 "?" 与省略号）、
// 路径中段折叠成 …、文件名尽量保留；短地址原样返回。
//
// 变异验证：ElideURL 直接返回 raw（截断撤掉）→ 后面几条断言红。
func TestElideURL(t *testing.T) {
	const width = 72
	short := "https://cdn.example.com/a.m4s"
	if got := ElideURL(short, width); got != short {
		t.Errorf("短地址不该被改动：%q", got)
	}

	withQuery := short + "?token=" + strings.Repeat("x", 200)
	got := ElideURL(withQuery, width)
	if strings.Contains(got, "token=") {
		t.Errorf("查询串应当整段丢掉：%q", got)
	}
	if !strings.Contains(got, "?…") {
		t.Errorf("丢掉查询串后要留下 ?… 让用户看出地址不完整：%q", got)
	}
	if DisplayWidth(got) > width {
		t.Errorf("截断后仍超过 %d 列：%q", width, got)
	}

	long := "https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/31/21/62131/62131_da3-1-100023.m4s?e=abc&deadline=1"
	got = ElideURL(long, width)
	if strings.Contains(got, "e=abc") {
		t.Errorf("查询串应当整段丢掉：%q", got)
	}
	if !strings.Contains(got, "/…/") || !strings.HasSuffix(got, "62131_da3-1-100023.m4s") {
		t.Errorf("应保留主机、折叠中段路径、保留文件名：%q", got)
	}
	if w := DisplayWidth(got); w > width {
		t.Errorf("截断后 %d 列，超过上限 %d：%q", w, width, got)
	}
}
