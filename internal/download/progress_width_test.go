package download

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// 进度帧的宽度契约：整帧（缩进 + 进度条 + 动画字符 + 百分比 + 速率 + ETA + 总量）必须落在
// 终端宽度之内，放不下时按「ETA → 总量 → 速率」的顺序裁，而不是让帧折行——进度帧是
// 原地重绘的，一旦折行，残帧会留在屏幕上（用户报的就是这个）。
//
// 宽度用 renderProgressFrameAt 显式给定，或用 withTerminalWidth 注入（见 progressline_test.go）。

// fullProgressFrame 是"满字段"的一帧：速率、ETA、总量都有。
func fullProgressFrame() progressFrame {
	const mib = 1 << 20
	return progressFrame{speedBps: 1.2 * mib, downloaded: 25 * mib, total: 100 * mib}
}

// frameBar 取出帧里 "[" 与 "]" 之间的进度条（"###---" 形态）；取不到直接失败。
func frameBar(t *testing.T, frame string) string {
	t.Helper()
	open := strings.IndexByte(frame, '[')
	closeIdx := strings.IndexByte(frame, ']')
	if open < 0 || closeIdx < open {
		t.Fatalf("帧里没有进度条：%q", frame)
	}
	bar := frame[open+1 : closeIdx]
	if strings.Trim(bar, "#-") != "" {
		t.Fatalf("进度条里混进了别的字符：%q", bar)
	}
	return bar
}

// TestProgressFrameFitsTerminalWidthAcrossWidths 五档宽度下整帧都不超过终端宽度，
// 且永远还是一行（含换行就会留下残帧）、永远带百分比与进度条。
//
// 变异验证：把 renderProgressFrameAt 换回固定 40 格（＝改前行为）→ 40/60/80 三档
// 的宽度断言立刻变红。
func TestProgressFrameFitsTerminalWidthAcrossWidths(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120, 200} {
		t.Run(fmt.Sprintf("%d列", width), func(t *testing.T) {
			frame := renderProgressFrameAt(width, fullProgressFrame(), '|')
			if w := DisplayWidth(frame); w > width {
				t.Errorf("宽度 %d 的终端里有 %d 列的帧（会折行留残帧）：%q", width, w, frame)
			}
			if strings.ContainsAny(frame, "\n\r") {
				t.Errorf("一帧必须是单行（原地重绘靠 \r）：%q", frame)
			}
			if !strings.Contains(frame, "25.00%") {
				t.Errorf("百分比是这一行的下限，任何宽度都不能裁：%q", frame)
			}
			bar := frameBar(t, frame)
			if len(bar) < progressMinBlocks || len(bar) > progressBlocks {
				t.Errorf("%d 列：进度条 %d 格，期望落在 [%d, %d]", width, len(bar), progressMinBlocks, progressBlocks)
			}
		})
	}
}

// TestProgressFrameDropsInfoByPriority 裁剪顺序：ETA → 总量 → 速率（百分比永不裁）。
// 每一档的宽度都按"刚好放不下更宽的那档"挑，所以断言的是行为而不是实现细节。
func TestProgressFrameDropsInfoByPriority(t *testing.T) {
	cases := []struct {
		width   int
		eta     bool
		amounts bool
		speed   bool
		why     string
	}{
		{200, true, true, true, "宽终端：满字段"},
		{120, true, true, true, "120 列仍放得下 40 格条 + 满字段统计区"},
		{80, false, true, true, "先去掉 ETA（速率还在，用户能自己估）"},
		{60, false, false, true, "再去掉总量（百分比还在）"},
		{40, false, false, true, "让出缩进后保住速率"},
		{20, false, false, false, "最后才省略速率（只剩百分比与进度条）"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d列", tc.width), func(t *testing.T) {
			frame := renderProgressFrameAt(tc.width, fullProgressFrame(), '|')
			for _, field := range []struct {
				name string
				want bool
				mark string
			}{
				{"ETA", tc.eta, "ETA "},
				{"总量", tc.amounts, "25.0/100.0 MB"},
				{"速率", tc.speed, "MB/s"},
			} {
				if got := strings.Contains(frame, field.mark); got != field.want {
					t.Errorf("%d 列（%s）：%s 出现=%v，期望 %v：%q", tc.width, tc.why, field.name, got, field.want, frame)
				}
			}
			if w := DisplayWidth(frame); w > tc.width {
				t.Errorf("%d 列的帧有 %d 列：%q", tc.width, w, frame)
			}
		})
	}
}

// TestProgressFrameKeepsBarAndFillRatio 进度条既不能消失（放得下时不少于下限），
// 也要真实反映比例：50% 时 # 与 - 各占一半。
func TestProgressFrameKeepsBarAndFillRatio(t *testing.T) {
	const mib = 1 << 20
	for _, width := range []int{80, 120, 200} {
		frame := renderProgressFrameAt(width, progressFrame{downloaded: 50 * mib, total: 100 * mib}, '|')
		bar := frameBar(t, frame)
		if len(bar) < progressMinBlocks {
			t.Fatalf("%d 列：进度条只有 %d 格（下限 %d）：%q", width, len(bar), progressMinBlocks, frame)
		}
		half := len(bar) / 2
		if got := strings.Count(bar, "#"); got != half {
			t.Errorf("%d 列：50%% 时填充 %d 格，期望 %d（条长 %d）：%q", width, got, half, len(bar), frame)
		}
	}
}

// TestProgressFrameFitsEightyColumnTerminal 用户报的原始场景：80 列终端上满字段帧曾是
// 28 缩进 + 40 格条 + 44 列统计区 = 116 列，换行后进度条残帧留在屏上。现在 80 列下一帧
// 恰好放得下，被裁掉的是 ETA。
func TestProgressFrameFitsEightyColumnTerminal(t *testing.T) {
	frame := renderProgressFrameAt(80, fullProgressFrame(), '|')
	if w := DisplayWidth(frame); w > 80 {
		t.Fatalf("80 列终端上的帧有 %d 列（会折行留残帧）：%q", w, frame)
	}
	if strings.Contains(frame, "ETA") {
		t.Errorf("80 列放不下 ETA，应当先裁它：%q", frame)
	}
	if !strings.Contains(frame, "25.0/100.0 MB") || !strings.Contains(frame, "MB/s") {
		t.Errorf("总量与速率比 ETA 更该留下：%q", frame)
	}
}

// TestProgressFrameNeverExceedsWidth 是一条性质判据：1..300 每一档宽度 × 几种进度形态
// 下，整帧都不超过终端宽度。宽度表只覆盖五档，这条把 1 列、17 列这类边界与
// 「速率/总量未知」的帧也兜住（随机采样要跑很多次才碰到窄宽度，这里穷举）。
func TestProgressFrameNeverExceedsWidth(t *testing.T) {
	frames := []progressFrame{
		fullProgressFrame(),
		{},                        // 什么都没结算出来
		{downloaded: 1, total: 1}, // 刚好完成
		{speedBps: 0.5, downloaded: 1 << 40, total: 1 << 41}, // 大数字
		{speedBps: 1e9, downloaded: 0, total: 1 << 30},       // 高速率
	}
	for width := 1; width <= 300; width++ {
		for i, f := range frames {
			frame := renderProgressFrameAt(width, f, '|')
			if w := DisplayWidth(frame); w > width {
				t.Fatalf("宽度 %d（帧 #%d）渲染出 %d 列：%q", width, i, w, frame)
			}
		}
	}
}

// TestProgressFrameDefaultWidthIsEighty 不注入宽度时（go test 的 stdout 是管道 → 非 TTY）
// 帧按 80 列渲染：--progress-json 之外的管道场景不会因为"拿不到宽度"画出满屏的条。
func TestProgressFrameDefaultWidthIsEighty(t *testing.T) {
	restore := util.SetTerminalWidthForTest(0)
	defer restore()
	frame := renderProgressFrame(fullProgressFrame().downloaded, fullProgressFrame().total, fullProgressFrame().speedBps, '|')
	if w := DisplayWidth(frame); w > util.DefaultTerminalWidth {
		t.Errorf("默认（非 TTY）宽度下帧有 %d 列，超过 %d：%q", w, util.DefaultTerminalWidth, frame)
	}
}
