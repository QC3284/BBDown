package download

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// 本次改动的宽度契约：清单（表头 + 数据行 + -I 直链行）与进度帧都必须落在终端宽度之内，
// 放不下时按「省列 → 缩列宽 → 悬挂折行」的优先级降级，而不是把行撑到换行。
//
// 全部用例都用 withTerminalWidth 注入宽度（见 progressline_test.go）：go test 的 stdout 是
// 管道，不注入的话每个用例都只能看到 80 列那一档。

// longCDNURL 是一条真实形态的 CDN 直链：路径 + 600 字符查询串（实测 -I 的输出形态）。
func longCDNURL() string {
	return "https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/31/21/62131/62131_da3-1-100023.m4s?" +
		"e=ig8euxZM2rNcNbdlhoNvNC8BqJIzNbfqXBvEqxTEto8BTrNvN0GvT90W5JZMkX_YN0MvXg8gNEV4NC8xNEV4N03eN0B5tZlqNxTEto8BTrNvNeZVuJ10Kj_g2UB02J0mN0B5tZlqNCNEto8BTrNvNC7MTX502C8f2jmMQJ6mqF2fka1mqx6gqj0eN0B599M=&mid=0&uipk=5&platform=pc&gen=playurlv3&os=cosbv&og=cos&trid=378cbbbfc33f4787b082faa3523c738u&deadline=1790444786&oi=29376629&nbs=1&upsig=f60bd148ff377c71f37f5595512e4a18&bw=207708&lrs=2&dl=0&orderid=0,3"
}

// widthSampleResult 是一份「真实形态」的清单夹具：中文清晰度、实测的 6 列帧率 "30.000"、
// 600+ 字符的直链，以及背景音频 / 配音 / 视频 / 音频四段。
func widthSampleResult() *entity.ParsedResult {
	return &entity.ParsedResult{
		BackgroundAudioTracks: []entity.Audio{{Codecs: "ec-3", Bandwidth: 448, Dur: 100}},
		RoleAudioList: []entity.AudioMaterialInfo{{
			Title: "中文",
			Audio: []entity.Audio{
				{Codecs: "mp4a.40.2", Bandwidth: 128, Dur: 100},
				{Codecs: "mp4a.40.2", Bandwidth: 64, Dur: 100},
			},
		}},
		VideoTracks: []entity.Video{
			{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30.000", Bandwidth: 3000, Dur: 100, BaseURL: longCDNURL()},
			{Dfn: "480P 清晰", Res: "854x480", Codecs: "AV1", FPS: "15.009", Bandwidth: 206, Dur: 100, BaseURL: longCDNURL()},
		},
		AudioTracks: []entity.Audio{{Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 100, BaseURL: longCDNURL()}},
	}
}

// tableLines 取出捕获输出里属于清单表体的行：表头、数据行、直链提示行。
//
// 带时间戳的事件行（"[日期 时间] - 共计N条视频流."）走的是全局日志通道，不在本次改动的
// 宽度契约里（改它等于改整个 CLI 的日志外观）；这里按时间戳前缀跳过，其余每一行都要满足
// 「≤ 终端宽度」。
func tableLines(t *testing.T, out string) []string {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if line == "" || strings.HasPrefix(line, "[") && strings.Contains(line, "] - ") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		t.Fatalf("没有捕获到清单行：%q", out)
	}
	return lines
}

// sectionHeader 挑出含 marker 的那条表头行（表头是每段清单的第一条表体行）。
func sectionHeader(t *testing.T, lines []string, marker string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("输出里没有含 %q 的表头行：%q", marker, lines)
	return ""
}

// dataRow 挑出含 marker 的那条数据行（唯一一条）。
func dataRow(t *testing.T, lines []string, marker string) string {
	t.Helper()
	got := ""
	for _, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		if got != "" {
			t.Fatalf("含 %q 的数据行不唯一：%q", marker, lines)
		}
		got = line
	}
	if got == "" {
		t.Fatalf("输出里没有含 %q 的数据行：%q", marker, lines)
	}
	return got
}

// TestTrackListFitsTerminalWidthAcrossWidths 是这次改动的核心判据：40/60/80/120/200 五档
// 宽度下，清单的每一行（表头、数据行、-I 直链提示行）都不超过终端宽度，且表头行存在、
// 列集合随宽度按优先级升降。
//
// 变异验证：把 newTrackPlan 的省列循环去掉（＝永远用全列）→ 60/80 两档的「每行 ≤ 宽度」
// 立即变红；把表头打印去掉 → 「表头行存在」变红。
func TestTrackListFitsTerminalWidthAcrossWidths(t *testing.T) {
	cases := []struct {
		width int
		// want 是该宽度下视频表头必须出现的列标签；absent 是必须不出现的。
		want   []string
		absent []string
	}{
		{40, []string{"清晰度", "码率"}, []string{"分辨率", "编码", "帧率", "体积"}},
		{60, []string{"清晰度", "分辨率", "编码", "码率"}, []string{"帧率", "体积"}},
		{80, []string{"清晰度", "分辨率", "编码", "码率"}, []string{"帧率", "体积"}},
		{120, []string{"清晰度", "分辨率", "编码", "帧率", "码率", "体积"}, nil},
		{200, []string{"清晰度", "分辨率", "编码", "帧率", "码率", "体积"}, nil},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d列", tc.width), func(t *testing.T) {
			withTerminalWidth(t, tc.width)
			out := stripANSI(captureStdout(t, func() { PrintAllTracks(widthSampleResult(), 100, false) }))
			lines := tableLines(t, out)

			for _, line := range lines {
				if w := DisplayWidth(line); w > tc.width {
					t.Errorf("宽度 %d 的终端里有 %d 列的行（会被折行）：%q", tc.width, w, line)
				}
			}

			header := sectionHeader(t, lines, "清晰度")
			for _, label := range tc.want {
				if !strings.Contains(header, label) {
					t.Errorf("%d 列的表头缺少 %q：%q", tc.width, label, header)
				}
			}
			for _, label := range tc.absent {
				if strings.Contains(header, label) {
					t.Errorf("%d 列的表头不该有 %q（放不下就要省列）：%q", tc.width, label, header)
				}
			}

			// 音频表头：只剩编码/码率两列时就该只有这两个标签（体积/分辨率等不占位）。
			audioHeader := sectionHeader(t, lines, "编码")
			if strings.Contains(audioHeader, "清晰度") {
				t.Errorf("音频表头不该出现视频专属列：%q", audioHeader)
			}
		})
	}
}

// TestTrackHeaderMatchesDataColumns 表头与数据行必须共用同一套列宽（单一来源）：
// 左对齐列（清晰度/分辨率）、右对齐列（帧率/码率/体积）的列位置都要严格对上，
// 否则表头就是在误导用户。
//
// 变异验证：把 headerLines 改成自己拼一套宽度（例如 fmt.Sprintf("%-12s")）→ 中文标签的
// 显示宽度差 2 列，本用例的列位置断言变红。
func TestTrackHeaderMatchesDataColumns(t *testing.T) {
	for _, width := range []int{60, 80, 120, 200} {
		t.Run(fmt.Sprintf("%d列", width), func(t *testing.T) {
			withTerminalWidth(t, width)
			out := stripANSI(captureStdout(t, func() { PrintAllTracks(widthSampleResult(), 100, false) }))
			lines := tableLines(t, out)
			header := sectionHeader(t, lines, "清晰度")
			row := dataRow(t, lines, "1920x1080")

			// 左对齐列：标签与值的起始显示列相同。
			for _, pair := range [][2]string{{"清晰度", "1080P 高清"}, {"分辨率", "1920x1080"}} {
				if strings.Contains(header, pair[0]) && strings.Contains(row, pair[1]) {
					if c1, c2 := displayColumn(t, header, pair[0]), displayColumn(t, row, pair[1]); c1 != c2 {
						t.Errorf("%d 列：%q 在第 %d 列、%q 在第 %d 列：\n%s\n%s", width, pair[0], c1, pair[1], c2, header, row)
					}
				}
			}
			// 右对齐列：标签与值的右边缘显示列相同。
			for _, pair := range [][2]string{{"帧率", "30.000"}, {"码率", "3000 kbps"}, {"体积", "~36.62 MB"}} {
				if strings.Contains(header, pair[0]) && strings.Contains(row, pair[1]) {
					if c1, c2 := displayColumnEnd(t, header, pair[0]), displayColumnEnd(t, row, pair[1]); c1 != c2 {
						t.Errorf("%d 列：%q 右边缘在第 %d 列、%q 在第 %d 列：\n%s\n%s", width, pair[0], c1, pair[1], c2, header, row)
					}
				}
			}
		})
	}
}

// TestTrackColumnsDegradeByPriority 省列顺序必须是「体积 → 帧率 → 编码 → 分辨率」，
// 且随宽度单调（更宽的终端不会反而少一列）。
func TestTrackColumnsDegradeByPriority(t *testing.T) {
	widths := []int{40, 60, 80, 120, 200}
	want := map[int][]string{
		40:  {"清晰度", "码率"},
		60:  {"清晰度", "分辨率", "编码", "码率"},
		80:  {"清晰度", "分辨率", "编码", "码率"},
		120: {"清晰度", "分辨率", "编码", "帧率", "码率", "体积"},
		200: {"清晰度", "分辨率", "编码", "帧率", "码率", "体积"},
	}
	labels := []string{"清晰度", "分辨率", "编码", "帧率", "码率", "体积"}

	var prev map[string]bool
	for _, width := range widths {
		withTerminalWidth(t, width)
		out := stripANSI(captureStdout(t, func() { PrintAllTracks(widthSampleResult(), 100, false) }))
		header := sectionHeader(t, tableLines(t, out), "清晰度")

		got := map[string]bool{}
		for _, label := range labels {
			if strings.Contains(header, label) {
				got[label] = true
			}
		}
		for _, label := range want[width] {
			if !got[label] {
				t.Errorf("%d 列：表头缺少 %q（该档位下它放得下）：%q", width, label, header)
			}
		}
		if prev != nil {
			for label := range prev {
				if !got[label] {
					t.Errorf("%d 列比更窄的档位还少一列 %q：宽终端不该反而少显示信息", width, label)
				}
			}
		}
		prev = got
	}
}

// TestTrackHeaderLabelsFitTheirColumns 表头标签必须放得进它那一列：放不下就会被截断
// （"帧…"）或把后面的列推歪。列宽是编译期常量，这条用例把"标签与列宽的配对"钉住——
// 改列宽而忘了改标签（或反过来）时它会先红。
func TestTrackHeaderLabelsFitTheirColumns(t *testing.T) {
	plan := newTrackPlan(200) // 全列档位
	for _, col := range plan.columns {
		for name, labels := range map[string]map[string]string{"视频": videoTrackHeaders, "音频": audioTrackHeaders} {
			label := labels[col.key]
			if label == "" {
				continue
			}
			if w := DisplayWidth(label); w > col.width {
				t.Errorf("%s表头标签 %q 占 %d 列，而 %s 列只有 %d 列宽——表头会被截断或错位",
					name, label, w, col.key, col.width)
			}
		}
	}
}

// TestTrackNarrowWidthKeepsReadableColumns 窄终端不得退化成「整行只剩一个 …」：
// 清晰度与码率（选流的两条主判据）任何宽度下都要在，数据行里至少要看得见清晰度名的前缀。
func TestTrackNarrowWidthKeepsReadableColumns(t *testing.T) {
	for _, width := range []int{20, 40, 60, 80} {
		t.Run(fmt.Sprintf("%d列", width), func(t *testing.T) {
			plan := newTrackPlan(width)
			have := map[string]bool{}
			for _, col := range plan.columns {
				have[col.key] = true
			}
			if !have[colName] || !have[colKbps] {
				t.Fatalf("%d 列丢了选流主判据（清晰度/码率）：%v", width, plan.columns)
			}

			lines := plan.videoLines("0.", entity.Video{
				Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30.000",
				Bandwidth: 3000, Dur: 100,
			}, 100)
			if len(lines) == 0 {
				t.Fatal("没有渲染出任何行")
			}
			for _, ln := range lines {
				text := strings.TrimSpace(ln.text)
				if text == "" || text == "…" {
					t.Errorf("%d 列退化成空行/只剩省略号：%q", width, ln.text)
				}
				if w := ln.indent + DisplayWidth(ln.text); w > width {
					t.Errorf("%d 列里有 %d 列的行：%q", width, w, ln.text)
				}
			}
			if got := strings.Join(planLines(lines), "\n"); !strings.Contains(got, "1080P") {
				t.Errorf("%d 列看不到清晰度名：%q", width, got)
			}
		})
	}
}

// TestTrackRowWrapsWithHangingIndent 折行是最后一招（宽度 < 26 列才会用到）：
// 续行必须悬挂缩进对齐到第一列数据，而不是顶格——顶格的续行会被读成新的一条流。
//
// 变异验证：把 layoutCells 的续行缩进去掉（顶格续行）→ 缩进断言变红。
func TestTrackRowWrapsWithHangingIndent(t *testing.T) {
	const width = 20
	plan := newTrackPlan(width)
	lines := plan.videoLines("0.", entity.Video{
		Dfn: "1080P 高帧率", Res: "1920x1080", Codecs: "HEVC", FPS: "60.000",
		Bandwidth: 20000, Dur: 100,
	}, 100)
	if len(lines) < 2 {
		t.Fatalf("%d 列放不下整行（首行 %q），应当折行", width, planLines(lines))
	}
	if lines[0].indent != plan.indent {
		t.Errorf("首行缩进 %d，期望 %d", lines[0].indent, plan.indent)
	}
	wantHang := plan.indent + plan.columns[0].width + trackColumnSepWidth
	for _, ln := range lines[1:] {
		if ln.indent != wantHang {
			t.Errorf("续行缩进 %d，期望悬挂缩进 %d（对齐到第一列数据）：%q", ln.indent, wantHang, ln.text)
		}
	}
	for _, ln := range lines {
		if w := ln.indent + DisplayWidth(ln.text); w > width {
			t.Errorf("%d 列里有 %d 列的行：%q", width, w, ln.text)
		}
		if strings.TrimSpace(ln.text) == "" {
			t.Errorf("折行折出了空行：%q", planLines(lines))
		}
	}
}

// TestOnlyShowInfoKeepsRawURLInPipe -I 的直链在非终端（管道/重定向）下逐字节原样：
// 独占一行、无缩进、无省略——脚本按行取地址是本仓的对外契约。
func TestOnlyShowInfoKeepsRawURLInPipe(t *testing.T) {
	raw := longCDNURL()
	result := widthSampleResult()
	out := stripANSI(captureStdout(t, func() { PrintAllTracks(result, 100, true) }))

	if !strings.Contains(out, "\n"+raw+"\n") {
		t.Errorf("-I 的直链在管道里必须逐字节原样、独占一行：\n%s", out)
	}
	if strings.Contains(out, "↳") || strings.Contains(out, "?…") {
		t.Errorf("管道里不该出现终端专用的省略形式：\n%s", out)
	}
}

// TestOnlyShowInfoElidesURLOnTerminal 终端里 600+ 字符的裸直链换成省略形式：
// 保留 host 与文件名尾部，整行不超过终端宽度。
//
// 变异验证：把 printStreamURL 的 TTY 分支改成 fmt.Println（＝把管道那套用到终端）
// → 本用例的「不该出现完整直链」变红；反过来把管道分支改成省略形式 → 上一条用例变红。
func TestOnlyShowInfoElidesURLOnTerminal(t *testing.T) {
	withFakeTerminal(t)
	withTerminalWidth(t, 80)
	raw := longCDNURL()
	out := stripANSI(captureStdout(t, func() { PrintAllTracks(widthSampleResult(), 100, true) }))

	if strings.Contains(out, raw) {
		t.Errorf("终端里不该把 600+ 字符的裸直链插进清单：\n%s", out)
	}
	if strings.Contains(out, "deadbeef") || strings.Contains(out, "upsig") {
		t.Errorf("省略形式应丢掉查询串主体：\n%s", out)
	}
	if !strings.Contains(out, "↳ ") {
		t.Errorf("终端里的直链应带 ↳ 提示行：\n%s", out)
	}
	if !strings.Contains(out, "upos-sz-mirrorcos.bilivideo.com") || !strings.Contains(out, ".m4s") {
		t.Errorf("省略形式必须保留 host 与文件名尾部：\n%s", out)
	}
	for _, line := range tableLines(t, out) {
		if w := DisplayWidth(line); w > 80 {
			t.Errorf("直链提示行超过 80 列（%d 列）：%q", w, line)
		}
	}
}

// TestElideURLKeepsWidthAndIdentity elideURL 的两条硬性质：结果 ≤ width，
// 且在有空间时保留 host 与文件名尾部（识别一条流靠的就是这两样）。
func TestElideURLKeepsWidthAndIdentity(t *testing.T) {
	raw := longCDNURL()

	if got := elideURL("https://cdn/v.m4s", 40); got != "https://cdn/v.m4s" {
		t.Errorf("放得下的短直链应原样返回，实际 %q", got)
	}
	for width := 8; width <= 96; width++ {
		got := elideURL(raw, width)
		if w := DisplayWidth(got); w > width {
			t.Errorf("width=%d 时 elideURL 返回 %d 列：%q", width, w, got)
		}
		if !strings.Contains(got, "…") {
			t.Errorf("width=%d 时省略形式没给出省略号：%q", width, got)
		}
	}
	got := elideURL(raw, 60)
	if !strings.Contains(got, "upos-sz-mirrorcos.bilivideo.com") {
		t.Errorf("省略形式丢了 host：%q", got)
	}
	if !strings.Contains(got, ".m4s") {
		t.Errorf("省略形式丢了文件名尾部：%q", got)
	}
	if strings.Contains(got, "upsig") {
		t.Errorf("省略形式该丢掉几百字符的查询串：%q", got)
	}
}
