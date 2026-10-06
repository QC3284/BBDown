package download

import (
	"strings"
	"testing"

	"github.com/QC3284/bbdown-go/internal/entity"
	"github.com/QC3284/bbdown-go/internal/util"
)

// stripANSI 去掉日志上的颜色码：LogColorNoTime 给每行套了 AnsiCyan/AnsiReset。
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

// layoutLines 取出捕获输出里的流清单行：过 ANSI、去日志缩进（LogColorNoTime 前缀 28 空格）。
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
		{"[视频]", 6},       // 选中轨道的标签
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

// TestTrackLinesUseBBDownTStyle 钉住 BBDownT 口径的**逐字**形态（本次改动的核心判据）：
//
//  0. [1080P 高码率] [1920x1080] [HEVC] [60] [~4194 kbps] [200.00 MB]
//     [视频] [1080P 高码率] [1920x1080] [HEVC] [60] [~4194 kbps] [200.00 MB]
//     [音频] [mp4a.40.2] [~132 kbps] [6.29 MB]
//
// 三条硬口径（都与上游 Display.cs 的形态不同，属有意偏离，收尾登记台账）：
//  1. 每个字段一格方括号，序号 "0." 与选中标签 [视频]/[音频] 裸写（序号不该再包一层）；
//  2. 码率是**由体积反推**的平均码率（~4194 而非接口声明的 5000）——同一行的体积与码率
//     因此永远自洽；估算行上（体积也是估的）反推值等于声明值（音频行 ~132 kbps）；
//  3. **体积不带 ~**（对齐 BBDownT：估算值也照显，不额外标注）。
//
// 变异验证：trackKbps 改回返回声明的 bandwidth → 第一/二行的 ~4194、~3104 变红；
// trackSizeCell 加回 \"~\" 前缀 → 体积断言变红；fitTrackCell 补空格（回旧排版）→ 逐字断言变红。
func TestTrackLinesUseBBDownTStyle(t *testing.T) {
	withTerminalWidth(t, 200)
	indent := strings.Repeat(" ", util.LogIndentWidth)

	// 接口给了 size（playurl 的真实形态）：码率由它反推，与声明的 5000 kbps 不同。
	video := entity.Video{Dfn: "1080P 高码率", Res: "1920x1080", Codecs: "HEVC", FPS: "60",
		Bandwidth: 5000, Dur: 400, Size: 209715200} // 200.00 MB；200MiB×8/400s = 4194 kbps
	wantVideo := indent + "0. [1080P 高码率] [1920x1080] [HEVC] [60] [~4194 kbps] [200.00 MB]"
	if got := formatVideoTrackLine(0, video, 400); got != wantVideo {
		t.Errorf("视频行逐字不符：\n got %q\nwant %q", got, wantVideo)
	}
	if strings.Contains(formatVideoTrackLine(0, video, 400), "5000 kbps") {
		t.Error("码率没有被体积反推：仍是接口声明的 bandwidth")
	}

	// 音频：没有真实 size，体积与码率互为逆运算，反推值 == 声明值。
	audio := entity.Audio{Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 400} // 400×132kbps×1000/8 = 6.29 MB
	wantAudio := indent + "0. [mp4a.40.2] [~132 kbps] [6.29 MB]"
	if got := formatAudioTrackLine(0, audio, 400); got != wantAudio {
		t.Errorf("音频行逐字不符：\n got %q\nwant %q", got, wantAudio)
	}

	// 选中行：行首是 [视频]/[音频] 标签（调用方给的），后面与清单行同口径。
	sel := stripANSI(captureStdout(t, func() { PrintSelectedTrack(&video, &audio, 400) }))
	wantSel := []string{
		indent + "[视频] [1080P 高码率] [1920x1080] [HEVC] [60] [~4194 kbps] [200.00 MB]",
		indent + "[音频] [mp4a.40.2] [~132 kbps] [6.29 MB]",
	}
	for _, want := range wantSel {
		if !strings.Contains(sel, want+"\n") {
			t.Errorf("选中行缺少逐字形态 %q：\n%s", want, sel)
		}
	}

	// 体积一律不带 ~（估算行也不例外）：估算体积的轨道也要照显。
	estimated := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60}
	if line := formatVideoTrackLine(0, estimated, 60); !strings.Contains(line, "[732.42 KB]") {
		t.Errorf("估算体积没有照显（或仍带 ~）：%q", line)
	}
	// 全行只有码率那一格带 ~（体积格不许有）：
	if n := strings.Count(formatVideoTrackLine(0, estimated, 60), "~"); n != 1 {
		t.Errorf("整行出现 %d 个 ~（应当只有码率格一个，体积格不带）：%q", n, formatVideoTrackLine(0, estimated, 60))
	}
	if strings.Contains(formatVideoTrackLine(0, estimated, 60), "[~732.42") {
		t.Errorf("体积格带回了 ~ 前缀：%q", formatVideoTrackLine(0, estimated, 60))
	}
}

// TestTrackCellsAreBracketedWithoutPadding 取代旧的「补空格把列对齐」判据（有意偏离，理由在
// tracklayout.go 顶部）：新口径是 BBDownT 的 [值] [值]，**括号内不补空格**，空列整格省略。
//
// 三条反转（旧用例曾经断言的是它们的反面）：
//  1. 方括号必须出现（旧：清单行不该再有方括号）；
//  2. 括号内不补空格（[60] 而不是 [    60]）；
//  3. 缺列的行比全列的行**短**（旧：空列不塌陷、各行等宽）。
//
// 为什么放弃对齐：单元格自带定界符后，视觉分组由方括号提供；再按列宽补空格会把每行多撑
// 2 列/字段（六列多 12 列），窄终端上反而更早触发省列。宽度硬约束仍由 layoutCells 保证
// （每行 ≤ 终端列数，见 tracklayout_width_test.go）。
//
// 变异验证：fitTrackCell 加回按列宽补空格 → 本用例红。
func TestTrackCellsAreBracketedWithoutPadding(t *testing.T) {
	withTerminalWidth(t, 200)
	narrow := entity.Video{Dfn: "高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100}
	wide := entity.Video{Dfn: "AB", Res: "854x480", Codecs: "HEVC", FPS: "60", Bandwidth: 3000, Dur: 100}
	lineA := formatVideoTrackLine(0, narrow, 100)
	lineB := formatVideoTrackLine(1, wide, 100)
	t.Logf("A（%d 列）%s", DisplayWidth(lineA), lineA)
	t.Logf("B（%d 列）%s", DisplayWidth(lineB), lineB)

	for _, line := range []string{lineA, lineB} {
		if !strings.Contains(line, "[") || !strings.Contains(line, "]") {
			t.Errorf("清单行必须用方括号分格：%q", line)
		}
		if strings.Contains(line, "[ ") || strings.Contains(line, " ]") {
			t.Errorf("方括号内侧不该有补出来的空格（不补空格是新口径）：%q", line)
		}
	}
	if !strings.Contains(lineB, "[60]") {
		t.Errorf("帧率格应当是 [60]（无补空格）：%q", lineB)
	}

	// 缺列（没有分辨率/帧率）的行整格省略，因此比全列行短——旧口径要求等宽，本次反转。
	bare := formatVideoTrackLine(2, entity.Video{Dfn: "1080P 高清", Codecs: "AVC", Bandwidth: 3000, Dur: 100}, 100)
	if wBare, wA := DisplayWidth(bare), DisplayWidth(lineA); wBare >= wA {
		t.Errorf("缺列行 %d 列，不该不短于全列行 %d 列：%q", wBare, wA, bare)
	}
	if strings.Contains(bare, "[]") {
		t.Errorf("空列被写成了空方括号（应当整格省略）：%q", bare)
	}
}

// TestTrackSectionsShareTheSameColumnSet 取代旧的「音频行与选中行共用视频列宽」判据（有意偏离）：
// 新口径下单元格自带方括号、不补空格，各段的同名列宽度本来就不同，没有可共用的列宽。
// 这条用例改钉真正要保证的性质：同一份清单里各段/各行的**列集合**来自同一个 plan——
//   - 音频段只有 [编码] [码率] [体积] 三格（空列整格省略，不占位、不写 []）；
//   - 选中行与清单行的数据格数相同（行首的 [视频]/[音频] 标签不算数据格）。
//
// 变异验证：让 audioCells 也输出分辨率/帧率等空占位列（写 [] 或补空格）→ 格数断言红。
func TestTrackSectionsShareTheSameColumnSet(t *testing.T) {
	withTerminalWidth(t, 200) // 全列档位：体积列不会被省掉，才能谈「音频段三格」
	video := entity.Video{ID: "80", Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100, BaseURL: "https://cdn/v.m4s"}
	audio := entity.Audio{ID: "30280", Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 100, BaseURL: "https://cdn/a.m4s"}

	listOut := captureStdout(t, func() {
		PrintAllTracks(&entity.ParsedResult{VideoTracks: []entity.Video{video}, AudioTracks: []entity.Audio{audio}}, 100, false)
	})
	selOut := captureStdout(t, func() { PrintSelectedTrack(&video, &audio, 100) })
	listLines := layoutLines(listOut)
	selLines := layoutLines(selOut)

	listVideo := findLayoutLine(t, listLines, "1920x1080")
	listAudio := findLayoutLine(t, listLines, "mp4a.40.2")
	selVideo := findLayoutLine(t, selLines, "[视频]")
	selAudio := findLayoutLine(t, selLines, "[音频]")

	// dataCells 数一行里的**数据格**：行首的 [视频]/[音频] 标签不算。
	dataCells := func(line string) int {
		n := strings.Count(line, "[")
		if strings.HasPrefix(line, "[视频]") || strings.HasPrefix(line, "[音频]") {
			n--
		}
		return n
	}
	if a, b := dataCells(listVideo), dataCells(selVideo); a != b {
		t.Errorf("清单视频行 %d 格、选中视频行 %d 格：两处必须用同一套列：\n%s\n%s", a, b, listVideo, selVideo)
	}
	if a, b := dataCells(listAudio), dataCells(selAudio); a != b {
		t.Errorf("清单音频行 %d 格、选中音频行 %d 格：两处必须用同一套列：\n%s\n%s", a, b, listAudio, selAudio)
	}
	if n := dataCells(listAudio); n != 3 {
		t.Errorf("音频行应只有 3 格（编码/码率/体积），实际 %d：%q", n, listAudio)
	}
	for _, line := range []string{listAudio, selAudio, listVideo, selVideo} {
		if strings.Contains(line, "[]") {
			t.Errorf("出现空方括号（空列应当整格省略）：%q", line)
		}
	}
}

// TestTrackLinesCarryEveryField 信息一条不减：清晰度/分辨率/编码/帧率/码率/体积仍在行里，
// 且每个字段都是 [值] 形态——**反转**旧用例的「清单行不该再有方括号」（本次口径就是要方括号）。
//
// 200 列：只有宽到放得下全部六列时才谈得上「一条不减」；窄终端按宽度自适应省列
// （见 tracklayout_width_test.go 的宽度表）。
//
// 变异验证：把 colSize 的方括号去掉（或把序号也包进方括号）→ 本用例红。
func TestTrackLinesCarryEveryField(t *testing.T) {
	withTerminalWidth(t, 200)
	video := entity.Video{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100}
	line := formatVideoTrackLine(2, video, 100)
	for _, want := range []string{"2.", "[1080P 高清]", "[1920x1080]", "[AVC]", "[30]", "[~3000 kbps]", "[35.76 MB]"} {
		if !strings.Contains(line, want) {
			t.Errorf("视频行缺少 %q：%q", want, line)
		}
	}
	// 序号裸写（不是 [2.]）：BBDownT 口径里行首序号没有方括号。
	if strings.Contains(line, "[2.]") {
		t.Errorf("行首序号不该带方括号：%q", line)
	}

	audio := entity.Audio{Codecs: "M4A", Bandwidth: 192, Dur: 100}
	aline := formatAudioTrackLine(0, audio, 100)
	for _, want := range []string{"0.", "[M4A]", "[~192 kbps]", "[2.29 MB]"} {
		if !strings.Contains(aline, want) {
			t.Errorf("音频行缺少 %q：%q", want, aline)
		}
	}
}

// TestTrackLineDurationFallback 体积/码率的取值规则：分P时长优先、缺失时退回轨道时长；
// 接口给了 size 就用它（不再估算）；码率一律由显示体积反推（1000 换算，与估算互为逆运算），
// 体积不带 ~。
//
// 200 列：体积列是宽度不足时**第一个**被省掉的列，窄终端上它根本不显示。
//
// 变异验证：体积估算改回上游的 1024 换算 → [732.42 KB] / [1.43 MB] 两处变红。
func TestTrackLineDurationFallback(t *testing.T) {
	withTerminalWidth(t, 200)
	v := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60}
	if line := formatVideoTrackLine(0, v, 0); !strings.Contains(line, "[732.42 KB]") { // 60×100kbps×1000/8 = 750000
		t.Errorf("分P时长缺失时应按轨道时长估算体积：%q", line)
	}
	if line := formatVideoTrackLine(0, v, 120); !strings.Contains(line, "[1.43 MB]") { // 120×100×1000/8 = 1500000
		t.Errorf("分P时长存在时应优先使用：%q", line)
	}
	// 接口给出 size 时不再估算：体积照它显示，码率由它反推。
	withSize := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60, Size: 10485760}
	line := formatVideoTrackLine(0, withSize, 0)
	if !strings.Contains(line, "[10.00 MB]") {
		t.Errorf("接口给出 size 时不该再估算：%q", line)
	}
	if !strings.Contains(line, "[~1398 kbps]") { // 10485760×8/60/1000 = 1398.1
		t.Errorf("码率应由给出的 size 反推（不是声明值）：%q", line)
	}
	a := entity.Audio{Codecs: "M4A", Bandwidth: 192, Dur: 100}
	if line := formatAudioTrackLine(0, a, 0); !strings.Contains(line, "[2.29 MB]") {
		t.Errorf("音频分P时长缺失时应按轨道时长估算体积：%q", line)
	}
}

// TestOnlyShowInfoKeepsRawURL -I 模式下每条流后的直链行保持原样：独占一行、无缩进、无补空格，
// 脚本按行取地址（这次只重排流清单，直链行不动）。
func TestOnlyShowInfoKeepsRawURL(t *testing.T) {
	result := &entity.ParsedResult{
		VideoTracks: []entity.Video{{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100, BaseURL: "https://cdn/v.m4s"}},
		AudioTracks: []entity.Audio{{Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 100, BaseURL: "https://cdn/a.m4s"}},
	}
	out := stripANSI(captureStdout(t, func() { PrintAllTracks(result, 100, true) }))
	for _, url := range []string{"https://cdn/v.m4s", "https://cdn/a.m4s"} {
		if !strings.Contains(out, "\n"+url+"\n") {
			t.Errorf("-I 的直链 %q 必须是独占一行、无缩进、无补空格的原文：\n%s", url, out)
		}
	}

	off := stripANSI(captureStdout(t, func() { PrintAllTracks(result, 100, false) }))
	if strings.Contains(off, "https://cdn/") {
		t.Errorf("未开 -I 不应出现直链行：\n%s", off)
	}
}
