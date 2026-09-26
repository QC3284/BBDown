package util

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

// 终端宽度的唯一来源。
//
// 为什么要一个可注入的接缝（SetTerminalWidthForTest/TerminalWidth）：流清单与进度帧的排版
// 都按终端宽度自适应，而 go test 的 stdout 是管道、默认宽度恒为 80——宽度相关的行为如果
// 只能靠真终端验证，就等于没有回归网。注入后，40/60/80/120/200 这些宽度都是用例的输入。
//
// 为什么在 util 而不是 download：宽度是所有「往终端写文本」的模块共享的环境事实
// （internal/workflow 的任务卡也按显示宽度排版），放在 util 避免各包自己养一份会走样的探测。

// DefaultTerminalWidth 是拿不到终端尺寸时的兜底宽度（列）。
//
// 非 TTY（管道/重定向）也走它：脚本与 CI 里没有「终端宽度」这回事，固定 80 列让同一份输入
// 在任何环境下产出同一份文本——这是机器可读输出（-I 的裸直链、--progress-json 的周边）
// 可预测的前提。
const DefaultTerminalWidth = 80

// LogIndentWidth 是「无时间戳」内容行（LogColorNoTime）的行首缩进：与带时间戳的日志前缀
// "[2006-01-02 15:04:05.000] - "（恰好 28 列）对齐，清单/表头/任务卡因此落在同一条竖线上。
//
// 它是**默认值**而不是硬约束：窄终端下 28 列缩进会把数据列挤出屏幕（40 列终端只剩 12 列），
// 此时排版层可以按宽度回收缩进（见 download.trackIndentFor），但宽终端上分毫不动。
const LogIndentWidth = 28

var (
	terminalWidthMu       sync.RWMutex
	terminalWidthOverride int
)

// stdoutIsTerminal 判定 stdout 是不是终端。做成变量是为了让用例注入：
// Windows 上 ioctl 不可用、CI 里 stdout 是管道，两条回落路径（COLUMNS / 默认 80）都靠它覆盖。
var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// terminalSize 探测 stdout 的终端尺寸。默认走 x/term（Unix 上是 TIOCGWINSZ ioctl，Windows 上
// 是控制台 API）；拿不到（重定向、句柄不支持、尺寸为 0）时返回 ok=false，由调用方回落到
// COLUMNS 或默认宽度。同样做成变量：跨平台用例要能强制「ioctl 不可用」这条路径。
var terminalSize = func(fd int) (width, height int, ok bool) {
	w, h, err := term.GetSize(fd)
	if err != nil || w <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// SetTerminalWidthForTest 把宽度探测固定成 width 列，返回还原函数（调用方必须 defer 它）。
//
// 宽度是包级状态，不还原会跨用例泄漏：后面的用例看到的宽度就成了上一个用例留下的值。
func SetTerminalWidthForTest(width int) func() {
	terminalWidthMu.Lock()
	prev := terminalWidthOverride
	terminalWidthOverride = width
	terminalWidthMu.Unlock()
	return func() {
		terminalWidthMu.Lock()
		terminalWidthOverride = prev
		terminalWidthMu.Unlock()
	}
}

// TerminalWidth 返回本次渲染可用的终端显示列数。
//
// 顺序：用例注入值 → 终端尺寸（x/term / ioctl）→ COLUMNS 环境变量 → DefaultTerminalWidth。
// 非 TTY（管道/重定向）不读 COLUMNS，直接 80：脚本里 COLUMNS 往往是交互式 shell 留下的残值，
// 用它会让同一份管道输入在不同机器上排成不同的样子。
//
// 不做缓存（每次渲染取一次即可）：终端可以在运行期被拖动改变尺寸，缓存只会让宽度过期。
func TerminalWidth() int {
	terminalWidthMu.RLock()
	override := terminalWidthOverride
	terminalWidthMu.RUnlock()
	if override > 0 {
		return override
	}
	if !stdoutIsTerminal() {
		return DefaultTerminalWidth
	}
	if w, _, ok := terminalSize(int(os.Stdout.Fd())); ok {
		return w
	}
	if w := columnsEnvWidth(); w > 0 {
		return w
	}
	return DefaultTerminalWidth
}

// columnsEnvWidth 解析 COLUMNS 环境变量（终端自己的宽度提示）。非正整数一律当没给：
// 空串、0、负数、"abc" 都不该把排版带偏。
func columnsEnvWidth() int {
	raw := strings.TrimSpace(os.Getenv("COLUMNS"))
	if raw == "" {
		return 0
	}
	w, err := strconv.Atoi(raw)
	if err != nil || w <= 0 {
		return 0
	}
	return w
}
