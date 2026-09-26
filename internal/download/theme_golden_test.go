package download

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// 本文件是流清单与进度条的**颜色角色 golden 守卫**：选中行是「BRAND 的 › + 整行 BOLD」、
// 表头整行 MUTED、数据行不着色、进度条填充 BRAND + 轨道 MUTED + 百分比 BOLD，
// 以及「NO_COLOR / 非 TTY / TERM=dumb 下这些渲染器一个 ANSI 都不发」。
//
// 断言写字面字节而不是引用常量：引用常量会让「实现与用例一起改」也判绿。

// colorsOn256 把「终端 + 256 色」钉死，用例结束自动还原。
func colorsOn256(t *testing.T) {
	t.Helper()
	t.Cleanup(util.ForceColorsForTest())
}

// sampleVideo 是排版用例常用的那条 480P 流（列宽判据与 tracklayout_test.go 同源）。
func sampleVideo() entity.Video {
	return entity.Video{Dfn: "480P 清晰", Res: "512x384", Codecs: "AV1", FPS: "15.009", Bandwidth: 206, Dur: 2055}
}

// TestSelectedRowIsBrandMarkerPlusBoldRow 钉住选中行的角色：行首 › 是 BRAND（不着色的是
// 序号列，两者同宽），整行 BOLD；未选中行是正文色，**一个色码都不发**。
//
// 变异验证：
//   - 把 selected 分支去掉（选中行不 BRAND/BOLD）→ 前两条断言红；
//   - 把 › 改回无色（TagText）→ marker 断言红。
func TestSelectedRowIsBrandMarkerPlusBoldRow(t *testing.T) {
	colorsOn256(t)
	v := sampleVideo()

	selected := formatVideoTrackRowLine(selectedMarker, true, v, 2055)
	styled := selected.Render()
	// 行首那一个空格是缩进（正文色、不发码），标记本身必须是 BRAND。
	if want := " \x1b[38;5;45m" + selectedMarker + "\x1b[0m"; !strings.HasPrefix(styled, want) {
		t.Errorf("选中行应以 BRAND 的 %q 开头：\n得到 %q\n期望前缀 %q", selectedMarker, styled, want)
	}
	if !strings.Contains(styled, "\x1b[1m") {
		t.Errorf("选中行整行应是 BOLD：%q", styled)
	}
	// 版式一字不变：去掉色码后与纯文本形态逐字节相同（列宽/空格都不受样式影响）。
	if got, want := stripANSI(styled), selected.Plain(); got != want {
		t.Errorf("选中行的着色形态与纯文本形态版式不一致：\n got %q\nwant %q", got, want)
	}
	if got, want := selected.Plain(), formatVideoTrackRow(selectedMarker, v, 2055); got != want {
		t.Errorf("选中行的版式应与非样式入口一致：\n got %q\nwant %q", got, want)
	}
	if got := selected.Plain(); !strings.HasPrefix(got, " "+selectedMarker+"  ") {
		t.Errorf("选中行应是「缩进 + › + 两个空格」：%q", got)
	}

	// 未选中行：整行正文色 → 无色码。
	plainRow := formatVideoTrackLineStyled(0, v, 2055).Render()
	if strings.Contains(plainRow, "\x1b") {
		t.Errorf("未选中的数据行不该带任何色码：%q", plainRow)
	}
	if got, want := plainRow, formatVideoTrackLine(0, v, 2055); got != want {
		t.Errorf("未选中行的着色形态应等于纯文本形态：\n got %q\nwant %q", got, want)
	}
}

// TestTrackHeaderIsMuted 钉住表头的角色：整行 MUTED（次要信息——它标注的是列，不是内容），
// 且不着重（没有 BOLD）。
//
// 变异验证：printTrackHeader 换成 util.Content（正文色）→ 色码断言红。
func TestTrackHeaderIsMuted(t *testing.T) {
	colorsOn256(t)
	out := captureStdout(t, func() { printTrackHeader(videoTrackHeader) })

	if want := "\x1b[38;5;245m"; !strings.HasPrefix(out, want) {
		t.Errorf("表头应以 MUTED 开头：%q", out)
	}
	if got, want := stripANSI(out), videoTrackHeader+"\n"; got != want {
		t.Errorf("表头的版式应保持不变：\n got %q\nwant %q", got, want)
	}
	if strings.Contains(out, "\x1b[1m") {
		t.Errorf("表头不该加粗（层级：表头是最次要的一层）：%q", out)
	}
}

// TestSectionBarIsBrand 钉住段标题的竖条走 BRAND、段名正文、计数 MUTED。
//
// 变异验证：Section.Line 里把竖条换成 TagText → 前缀断言红。
func TestSectionBarIsBrand(t *testing.T) {
	colorsOn256(t)
	line := Section{Name: "可用流", Count: "（6）"}.Line()
	want := "\x1b[38;5;45m▎\x1b[0m可用流\x1b[38;5;245m（6）\x1b[0m"
	if got := line.Render(); got != want {
		t.Errorf("段标题着色形态不符：\n got %q\nwant %q", got, want)
	}
	if got, want := line.Plain(), "▎可用流（6）"; got != want {
		t.Errorf("段标题纯文本形态不符：%q", got)
	}
}

// TestProgressFrameRolesAreGolden 钉住进度帧的角色：填充 BRAND、轨道 MUTED、百分比 BOLD、
// 其余（动画字符/速率/ETA/总量）MUTED；轨道字符在无色形态下也是 ░。
//
// 变异验证：把填充段换成 TagText → BRAND 断言红；去掉百分比段的 TagTextBold → BOLD 断言红。
func TestProgressFrameRolesAreGolden(t *testing.T) {
	colorsOn256(t)
	line := progressFrameLine(50<<20, 100<<20, 1<<20, '|')
	rendered := line.Render()

	if !strings.HasPrefix(rendered, "\x1b[38;5;45m"+strings.Repeat(progressFill, 10)+"\x1b[0m") {
		t.Errorf("进度条填充应是 BRAND：%q", rendered)
	}
	if want := "\x1b[38;5;245m" + strings.Repeat(progressTrack, 10); !strings.Contains(rendered, want) {
		t.Errorf("进度条轨道应是 MUTED：%q", rendered)
	}
	if want := "\x1b[1m  50.0%\x1b[0m"; !strings.Contains(rendered, want) {
		t.Errorf("百分比应是 BOLD：%q", rendered)
	}
	// 版式不变量：去掉色码后与 Plain 逐字节相同，且字形仍是 █/░。
	if got, want := stripANSI(rendered), line.Plain(); got != want {
		t.Errorf("进度帧两个形态版式不一致：\n got %q\nwant %q", got, want)
	}
	if !strings.HasPrefix(line.Plain(), strings.Repeat(progressFill, 10)+strings.Repeat(progressTrack, 10)) {
		t.Errorf("无色形态的进度条应保持 █/░ 两个字符：%q", line.Plain())
	}
}

// TestProgressFrameWidthIsStableAcrossZeroToHundred 是原地重绘的硬要求：从 0% 到 100% 的
// 每一帧，**显示列数必须完全一致**（同一组字段在场时）——列宽一变，上一帧的尾巴就擦不干净，
// 整行看起来在跳。
//
// 真机抓帧抓到过反例：百分比用 %5.1f 时最后一帧是 "100.0%"（6 列），整行被撑长一列，
// 右侧总量跟着跳一格。这条用例把那类「最后一帧才露头」的宽度问题钉在单元层。
//
// 变异验证：progressPercentFormat 改回 "%5.1f%%" → 100% 那一帧变宽，本用例红。
func TestProgressFrameWidthIsStableAcrossZeroToHundred(t *testing.T) {
	const total = 100 << 20
	presets := []struct {
		name  string
		speed float64
		total int64
	}{
		{"速率与总量已知", 1 << 20, total},
		{"速率已知总量未知", 1 << 20, 0},
		{"速率与总量都未知", 0, total},
	}
	for _, p := range presets {
		t.Run(p.name, func(t *testing.T) {
			width := -1
			for pct := 0; pct <= 100; pct++ {
				downloaded := int64(pct) * p.total / 100
				frame := renderProgressFrame(downloaded, p.total, p.speed, '|')
				w := DisplayWidth(frame)
				if width < 0 {
					width = w
					continue
				}
				if w != width {
					t.Fatalf("第 %d%% 帧宽 %d 列，与前面的 %d 列不一致（列在抖）：%q", pct, w, width, frame)
				}
			}
		})
	}
}

// TestDownloadRenderingStaysPlainWithoutColorCapability：清单 + 进度帧在最外层的三个降级
// 条件下**一个 ANSI 都不发**，且版式（段标题竖条、表头、›、█/░）一字不少。
//
// 变异验证：任一渲染入口绕过 util 的能力判定（自己拼 SGR）→ 本用例红。
func TestDownloadRenderingStaysPlainWithoutColorCapability(t *testing.T) {
	result := &entity.ParsedResult{
		VideoTracks: []entity.Video{sampleVideo()},
		AudioTracks: []entity.Audio{{Codecs: "mp4a.40.2", Bandwidth: 134, Dur: 2055}},
	}
	video := sampleVideo()

	cases := []struct {
		name string
		tty  bool
		env  util.ColorEnv
	}{
		{"非 TTY", false, util.ColorEnv{Term: "xterm-256color"}},
		{"NO_COLOR=1", true, util.ColorEnv{NoColor: true, Term: "xterm-256color"}},
		{"TERM=dumb", true, util.ColorEnv{Term: "dumb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreTerm := util.SetTerminalForTest(func() bool { return c.tty })
			restoreEnv := util.SetColorEnvForTest(func() util.ColorEnv { return c.env })
			defer func() { restoreEnv(); restoreTerm() }()

			out := captureStdout(t, func() {
				PrintAllTracks(result, 2055, false)
				PrintSelectedTrack(&video, nil, 2055)
				printTrackHeader(videoTrackHeader)
			})
			if strings.Contains(out, "\x1b") {
				t.Errorf("%s 下清单不该有任何 ANSI：%q", c.name, out)
			}
			for _, want := range []string{"▎可用流（1）", "▎已选择的视频流", " " + selectedMarker + "  ", "清晰度"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s 下缺少版式元素 %q：%q", c.name, want, out)
				}
			}
			if frame := renderProgressFrame(50<<20, 100<<20, 1<<20, '|'); strings.Contains(frame, "\x1b") {
				t.Errorf("%s 下进度帧不该有任何 ANSI：%q", c.name, frame)
			}
		})
	}
}
