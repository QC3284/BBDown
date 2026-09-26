package util

import (
	"testing"
)

// withTerminalProbe 把两条探测路径换成用例给定的实现：CI 里 stdout 是管道、Windows 上
// ioctl 不可用，真实探测的结果不可控，而终端宽度分支必须逐条覆盖（跨平台判据）。
func withTerminalProbe(t *testing.T, isTTY bool, sizeWidth int, sizeOK bool) {
	t.Helper()
	origTTY, origSize := stdoutIsTerminal, terminalSize
	stdoutIsTerminal = func() bool { return isTTY }
	terminalSize = func(int) (int, int, bool) {
		if !sizeOK {
			return 0, 0, false
		}
		return sizeWidth, 24, true
	}
	t.Cleanup(func() {
		stdoutIsTerminal = origTTY
		terminalSize = origSize
	})
}

// TestTerminalWidthPrefersTerminalSize 终端可用时以 ioctl/控制台 API 报的宽度为准，
// COLUMNS 只是它的回落——环境变量比真实尺寸更容易过期（改窗口大小后 shell 不一定更新它）。
func TestTerminalWidthPrefersTerminalSize(t *testing.T) {
	withTerminalProbe(t, true, 133, true)
	t.Setenv("COLUMNS", "97")
	if got := TerminalWidth(); got != 133 {
		t.Errorf("TerminalWidth() = %d，期望终端尺寸 133（COLUMNS=97 只能当回落）", got)
	}
}

// TestTerminalWidthFallsBackToColumns 取不到尺寸时用 COLUMNS：这正是 ioctl 在 Windows 上
// 不可用（或句柄不是控制台）时的路径。非正整数一律忽略，回落默认宽度。
func TestTerminalWidthFallsBackToColumns(t *testing.T) {
	withTerminalProbe(t, true, 0, false)
	cases := []struct {
		columns string
		want    int
	}{
		{"120", 120},
		{" 100 ", 100}, // 环境变量两侧的空白不该让解析失败
		{"1", 1},
		{"", DefaultTerminalWidth},
		{"0", DefaultTerminalWidth},
		{"-20", DefaultTerminalWidth},
		{"abc", DefaultTerminalWidth},
		{"80.5", DefaultTerminalWidth},
	}
	for _, tc := range cases {
		t.Setenv("COLUMNS", tc.columns)
		if got := TerminalWidth(); got != tc.want {
			t.Errorf("COLUMNS=%q 时 TerminalWidth() = %d，期望 %d", tc.columns, got, tc.want)
		}
	}
}

// TestTerminalWidthNonTTYUsesDefault 非 TTY（管道/重定向）按 80 处理：脚本与 CI 里没有
// 「终端宽度」，COLUMNS 是交互式 shell 的残值，跟着它排版会让同一份管道输入在不同机器上不同。
func TestTerminalWidthNonTTYUsesDefault(t *testing.T) {
	withTerminalProbe(t, false, 200, true) // 即使尺寸探测"成功"也不该采信
	t.Setenv("COLUMNS", "40")
	if got := TerminalWidth(); got != DefaultTerminalWidth {
		t.Errorf("非 TTY 下 TerminalWidth() = %d，期望 %d", got, DefaultTerminalWidth)
	}
}

// TestSetTerminalWidthForTestOverrides 注入的宽度优先于一切探测，并在还原后失效。
// 注入是包级状态，跨用例泄漏会让后面的用例看到上一个用例留下的宽度。
func TestSetTerminalWidthForTestOverrides(t *testing.T) {
	withTerminalProbe(t, true, 133, true)
	t.Setenv("COLUMNS", "97")

	restore := SetTerminalWidthForTest(45)
	if got := TerminalWidth(); got != 45 {
		t.Fatalf("注入后 TerminalWidth() = %d，期望 45", got)
	}
	restore()
	if got := TerminalWidth(); got != 133 {
		t.Errorf("还原后 TerminalWidth() = %d，期望回到终端尺寸 133", got)
	}
}

// TestDefaultTerminalWidthIsUsable 兜底宽度必须够放下一份清单：宽度表（40/60/80/120/200）
// 与告警文案都以 80 为「没有终端信息时的标准屏宽」。
func TestDefaultTerminalWidthIsUsable(t *testing.T) {
	if DefaultTerminalWidth != 80 {
		t.Errorf("DefaultTerminalWidth = %d，期望 80", DefaultTerminalWidth)
	}
	// 缩进不得超过兜底宽度的一半：否则 80 列终端上内容区会被 28 列缩进吃掉一大块。
	if LogIndentWidth*2 > DefaultTerminalWidth {
		t.Errorf("LogIndentWidth = %d 相对 %d 列的兜底宽度过大", LogIndentWidth, DefaultTerminalWidth)
	}
}
