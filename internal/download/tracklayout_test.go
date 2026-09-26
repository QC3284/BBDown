package download

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
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

// TestTrackLinesAlignByDisplayWidth 是这次排版的核心判据：标签显示宽度不同的两行，
// 整行显示宽度必须一致，且清晰度列、右对齐的码率列都落在同一显示列。
//
// 两个夹具的标签 rune 数相同（"高清" / "AB"）而显示宽度差 2 列，码率与体积的位数也不同——
// 只要补空格不是按 DisplayWidth 算（len/rune 数），下面任意一条断言都会红。
// 变异验证：PadDisplay 改成直接返回原串 → 变红。
func TestTrackLinesAlignByDisplayWidth(t *testing.T) {
	narrow := entity.Video{Dfn: "高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 1, Dur: 100}
	wide := entity.Video{Dfn: "AB", Res: "854x480", Codecs: "HEVC", FPS: "60", Bandwidth: 20000, Dur: 100}
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

	// 空列（没有分辨率/帧率的轨道）同样不塌陷、不甩出方括号兜底
	bare := formatVideoTrackLine(2, entity.Video{Dfn: "1080P 高清", Codecs: "AVC", Bandwidth: 3000, Dur: 100}, 100)
	if wBare, wA := DisplayWidth(bare), DisplayWidth(lineA); wBare != wA {
		t.Errorf("空列行宽度 %d 与其它行 %d 不一致：%q", wBare, wA, bare)
	}
}

// TestAudioAndSelectedLinesShareVideoLayout 音频行与 PrintSelectedTrack 的两行共用同一套列宽：
// 编码落在「清晰度」列，码率/体积列与视频行对齐（音频行留空的分辨率/编码/帧率三列就是占位）。
func TestAudioAndSelectedLinesShareVideoLayout(t *testing.T) {
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
		{"选中·视频", findLayoutLine(t, selLines, "[视频]")},
		{"选中·音频", findLayoutLine(t, selLines, "[音频]")},
	}
	wantWidth := DisplayWidth(lines[0].line)
	wantKbpsEnd := displayColumnEnd(t, lines[0].line, "kbps")
	for _, l := range lines {
		if got := DisplayWidth(l.line); got != wantWidth {
			t.Errorf("%s 行显示宽度 %d，与清单·视频行 %d 不一致：%q", l.name, got, wantWidth, l.line)
		}
		if got := displayColumnEnd(t, l.line, "kbps"); got != wantKbpsEnd {
			t.Errorf("%s 行码率列右边缘在第 %d 列，与清单·视频行第 %d 列不一致：%q", l.name, got, wantKbpsEnd, l.line)
		}
	}
}

// TestTrackLinesCarryEveryField 信息一条不减：清晰度/分辨率/编码/帧率/码率/体积仍在行里，
// 只是方括号换成了列间距（清单行不再出现 [ ]）。
func TestTrackLinesCarryEveryField(t *testing.T) {
	video := entity.Video{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100}
	line := formatVideoTrackLine(2, video, 100)
	for _, want := range []string{"2.", "1080P 高清", "1920x1080", "AVC", "30", "3000 kbps", "~36.62 MB"} {
		if !strings.Contains(line, want) {
			t.Errorf("视频行缺少 %q：%q", want, line)
		}
	}
	if strings.ContainsAny(line, "[]") {
		t.Errorf("视频清单行不该再有方括号：%q", line)
	}

	audio := entity.Audio{Codecs: "M4A", Bandwidth: 192, Dur: 100}
	aline := formatAudioTrackLine(0, audio, 100)
	for _, want := range []string{"0.", "M4A", "192 kbps", "~2.34 MB"} {
		if !strings.Contains(aline, want) {
			t.Errorf("音频行缺少 %q：%q", want, aline)
		}
	}
	if strings.ContainsAny(aline, "[]") {
		t.Errorf("音频清单行不该再有方括号：%q", aline)
	}
}

// TestTrackLineDurationFallback 体积的取值规则与改前一致：分P时长优先、缺失时用轨道时长；
// 接口给了 size 就不再估算。
func TestTrackLineDurationFallback(t *testing.T) {
	v := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60}
	if line := formatVideoTrackLine(0, v, 0); !strings.Contains(line, "~750.00 KB") { // 60×100kbps×1024/8 = 768000
		t.Errorf("分P时长缺失时应按轨道时长估算体积：%q", line)
	}
	if line := formatVideoTrackLine(0, v, 120); !strings.Contains(line, "~1.46 MB") { // 120×100×1024/8 = 1536000
		t.Errorf("分P时长存在时应优先使用：%q", line)
	}
	withSize := entity.Video{Dfn: "360P 流畅", Bandwidth: 100, Dur: 60, Size: 2048}
	if line := formatVideoTrackLine(0, withSize, 0); !strings.Contains(line, "~2.00 KB") {
		t.Errorf("接口给出 size 时不该再估算：%q", line)
	}
	a := entity.Audio{Codecs: "M4A", Bandwidth: 192, Dur: 100}
	if line := formatAudioTrackLine(0, a, 0); !strings.Contains(line, "~2.34 MB") {
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
