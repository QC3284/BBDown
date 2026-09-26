package util

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MaxLogFieldLen bounds one server-controlled value inside a log line, so a
// hostile response cannot flood the terminal or the log file.
const MaxLogFieldLen = 512

// 终端里仍然直接使用的两条 ANSI 序列。
//
// 颜色**角色**不在这里：CLI 的配色（BRAND/MUTED/TEXT + 三个状态色）与降级链都在
// theme.go，调用点一律用 util.TagXxx，不再各拼各的 SGR 序列。这里只剩二维码要用的
// 黑白前景（它是图形不是文字层级，不参与主题）与通用复位。
const (
	AnsiReset = "\033[0m"
	AnsiBlack = "\033[30m" // 二维码深色模块
	AnsiWhite = "\033[97m" // 二维码浅色模块 / 横幅遗留常量
)

// SanitizeLogString makes a value safe to interpolate into a single log line.
// Server-controlled text (video titles, uploader names, API messages, values
// derived from a request URL) can carry CR/LF and ANSI escapes that forge log
// lines or rewrite the terminal (upstream RF-54 / RF-70). Control characters
// become spaces and the result is truncated. Legitimate values pass through
// unchanged.
func SanitizeLogString(s string) string {
	if s == "" {
		return s
	}
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return truncateRunes(s, MaxLogFieldLen)
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			sb.WriteByte(' ')
			continue
		}
		sb.WriteRune(r)
	}
	return truncateRunes(strings.Join(strings.Fields(sb.String()), " "), MaxLogFieldLen)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// sanitizeLogArg neutralises the argument forms that can carry server text.
// Values are sanitised per argument rather than on the rendered line so that a
// caller's own intentional formatting (indentation, prefixes) is preserved.
func sanitizeLogArg(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		return SanitizeLogString(t)
	case error:
		return errors.New(SanitizeLogString(t.Error()))
	default:
		return v
	}
}

func sanitizeLogArgs(args []interface{}) []interface{} {
	out := make([]interface{}, len(args))
	for i, a := range args {
		out[i] = sanitizeLogArg(a)
	}
	return out
}

// ConsoleLock 串行化进程内的所有控制台写入：日志与进度条共用它。
//
// 上游 BBDown.Core.Logger.ConsoleLock 就是被 BBDown/Infrastructure/ProgressBar 复用的同一把锁；
// 不加锁时进度条重绘会与日志写入互相插入，终端上留下半行进度残影（实测：下载结束打
// 「合并分片...」时，上一帧的 25.07% 尾巴连同它一起显示成两行进度）。
var ConsoleLock sync.Mutex

// logSink 保存一次「日志让位」的目标流（见 RedirectConsoleLogs）。
type logSink struct{ w io.Writer }

// consoleLogSink 是当前生效的让位目标；nil = 没有让位。
//
// 用原子指针而不是普通变量：后台协程（如更新检查）也会写日志，而读取发生在 ConsoleLock 之外。
var consoleLogSink atomic.Pointer[logSink]

// consoleLogWriter 解析一条日志行的落点。
//
// 默认路径**每次写入都重新解析 os.Stdout**，而不是把它存在变量里：本仓测试的既有约定是
// 「替换 os.Stdout 捕获输出」（internal/cli 与 internal/util 各有一份 captureStdout）。
// 目标一旦被冻结在变量里，替换 os.Stdout 就再也截不到日志——f828cba 的坑之一。
func consoleLogWriter() io.Writer {
	if s := consoleLogSink.Load(); s != nil {
		return s.w
	}
	return os.Stdout
}

// RedirectConsoleLogs 在**本次作用域**内把日志行（含给进度条那一行收尾的换行）写到 w，
// 返回还原函数——调用方必须 defer 它。
//
// 用途是机读模式（--info-json / doctor --json）：日志让位到 stderr，stdout 只留数据，
// jq / 脚本才拿得到干净输入。之所以是「作用域」而不是全局开关：作用域退出即还原成调用前的
// 落点，作用域之外的代码与同包用例拿回 os.Stdout——全局可变目标会跨用例泄漏，让后来的用例
// 捕获到空串（f828cba 的坑之二）。
func RedirectConsoleLogs(w io.Writer) (restore func()) {
	prev := consoleLogSink.Swap(&logSink{w: w})
	return func() { consoleLogSink.Store(prev) }
}

// progressLineActive 表示终端当前行上停着一条尚未收尾的进度条。
var progressLineActive atomic.Bool

// SetProgressLineActive 由进度条在绘制/擦除时告知日志：这一行现在归进度条。
func SetProgressLineActive(active bool) { progressLineActive.Store(active) }

// consoleWrite 在 ConsoleLock 之下执行一次完整的控制台写入。
//
// 写日志前先给进度条那一行收尾（换行）：进度条是原地重绘的，日志若直接接在它后面，
// 下一帧重绘会回到行首把这条日志整行擦掉。
func consoleWrite(write func(w io.Writer)) {
	ConsoleLock.Lock()
	defer ConsoleLock.Unlock()
	w := consoleLogWriter()
	if progressLineActive.Swap(false) {
		fmt.Fprint(w, "\n")
	}
	write(w)
}

// Logger provides thread-safe, colored console logging with optional file output.
type Logger struct {
	mu          sync.Mutex
	logFilePath string
	debugMode   func() bool // callback to check if debug is enabled

	logFailures       int
	logSuspendedUntil time.Time
}

// NewLogger creates a new Logger.
func NewLogger(debugFn func() bool) *Logger {
	return &Logger{
		debugMode: debugFn,
	}
}

// SetLogFile sets the path for persistent log file output.
func (l *Logger) SetLogFile(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logFilePath = path
}

// logFailureCooldown is how long file logging is suspended after
// maxLogFailures consecutive write failures: a full or read-only volume would
// otherwise retry (and fail) on every single line.
const (
	maxLogFailures     = 5
	logFailureCooldown = 30 * time.Second
)

func (l *Logger) appendToFile(line string) {
	l.mu.Lock()
	path := l.logFilePath
	if path == "" || time.Now().Before(l.logSuspendedUntil) {
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()

	// The file is opened per line rather than kept open: Go already opens with
	// FILE_SHARE_READ|WRITE|DELETE, so a second instance is not locked out, and a
	// long-lived handle would go stale if the file is rotated or removed.
	if err := writeLine(path, line); err != nil {
		l.mu.Lock()
		l.logFailures++
		if l.logFailures >= maxLogFailures {
			l.logSuspendedUntil = time.Now().Add(logFailureCooldown)
			l.logFailures = 0
			fmt.Fprintf(os.Stderr, "日志文件写入连续失败，已暂停 %v: %s\n", logFailureCooldown, path)
		}
		l.mu.Unlock()
		return
	}

	l.mu.Lock()
	l.logFailures = 0
	l.mu.Unlock()
}

// writeLine appends one line, flushing before close.
func writeLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, line); err != nil {
		return err
	}
	return f.Sync()
}

// 事件时间戳的两档：默认只到时分秒（日期与毫秒对看日志的人没有价值，还把每行内容推后
// 16 列），--debug 下才给全量——排查时序问题时才需要日期与毫秒。
//
// 日志**文件**不受 --debug 影响，始终写全量：文件是事后对账用的，跨天的记录只留 HH:MM:SS
// 就没法定位，而终端上那 16 列每一行都在挤占内容。
const (
	eventTimeLayout      = "15:04:05"
	eventTimeDebugLayout = "2006-01-02 15:04:05.000"
	fileTimeLayout       = "2006-01-02 15:04:05.000"
)

// logClock 是事件时间戳的时钟。做成变量是为了让用例把「现在」钉死：毫秒与日期只在 --debug
// 下出现这条规则要靠断言钉住，就必须能控制时间（真实墙钟跑两次可能跨秒、跨毫秒）。
// 生产路径不设置它，就是 time.Now。
var logClock = time.Now

// SetLogClock 注入事件时间戳的时钟（fn 为 nil 表示还原真实墙钟），返回还原函数，调用方
// 必须 defer 它。跨包用例（internal/cli 的日志断言）也要用它。
func SetLogClock(fn func() time.Time) (restore func()) {
	prev := logClock
	if fn == nil {
		logClock = time.Now
	} else {
		logClock = fn
	}
	return func() { logClock = prev }
}

func (l *Logger) debug() bool { return l.debugMode != nil && l.debugMode() }

// eventPrefix 是终端上一条事件行的前缀（含尾随两个空格）：默认 "19:20:37  "，
// --debug 下是 "2026-09-26 19:20:37.865  "。
//
// 改前是方括号形态 "[19:20:37] "：时间戳是**次要信息**（主题里归 MUTED），方括号却给了它
// 与正文同等的视觉重量，还把每行内容推后两列。去掉方括号后前缀宽度不变（8+2 或 23+2），
// 消息仍然从同一列开始——列对齐是版式的一部分，不能因为掉括号就漂移。
func (l *Logger) eventPrefix(now time.Time) string {
	layout := eventTimeLayout
	if l.debug() {
		layout = eventTimeDebugLayout
	}
	return now.Format(layout) + "  "
}

// eventLine 组装一条事件行：MUTED 时间戳 + 正文；warn/error 另加符号与状态色。
//
// 符号（⚠ / ✗）是**版式**不是颜色：无色能力下（NO_COLOR / 管道 / TERM=dumb）它照旧出现，
// 层级与可扫读性不依赖颜色。日志文件拿到的是同一个纯文本视图（见各 Log* 方法）。
func (l *Logger) eventLine(now time.Time, tag Tag, marker, msg string) Line {
	return NewLine().Add(TagMuted, l.eventPrefix(now)).Add(tag, marker+msg)
}

// writeEvent 是事件通道的唯一出口：终端写着色视图，日志文件写纯文本视图（含全量时间戳）。
func (l *Logger) writeEvent(now time.Time, line Line, plain string) {
	consoleWrite(func(w io.Writer) { fmt.Fprint(w, line.Render()+"\n") })
	l.appendToFile(filePrefix(now) + plain)
}

// filePrefix 是日志文件里一行的前缀：始终全量时间戳（见上面的常量说明）。
func filePrefix(now time.Time) string { return "[" + now.Format(fileTimeLayout) + "] " }

// 事件行的符号：警告 ⚠、错误 ✗。它们与状态色一起出现，但本身是无色形态的一部分。
const (
	warnMarker  = "⚠ "
	errorMarker = "✗ "
)

// Log prints a normal log line.
func (l *Logger) Log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	now := logClock()
	l.writeEvent(now, l.eventLine(now, TagText, "", msg), msg)
}

// LogError prints an error line: ✗ + 错误色（消息本身也走错误色，状态一眼可辨）。
func (l *Logger) LogError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	now := logClock()
	l.writeEvent(now, l.eventLine(now, TagError, errorMarker, msg), errorMarker+msg)
}

// LogWarn prints a warning line: ⚠ + 警告色。
func (l *Logger) LogWarn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	now := logClock()
	l.writeEvent(now, l.eventLine(now, TagWarn, warnMarker, msg), warnMarker+msg)
}

// LogColor prints an attention line in BRAND (新版本、二维码过期这类「系统通知」：
// 不是错误也不是警告，但需要被看见)。
func (l *Logger) LogColor(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	now := logClock()
	l.writeEvent(now, l.eventLine(now, TagBrand, "", msg), msg)
}

// LogTagged 打一条事件行：MUTED 时间戳 + 指定样式（角色/加粗）的正文，**不加**状态标记。
//
// 给「自带状态列」的表格用（doctor 的 +/!/x 三档）：状态符号是那一列的内容，再套一个 ⚠/✗
// 会把那一列推歪、还变成双重标注。等级仍然由状态色表达。
func (l *Logger) LogTagged(tag Tag, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	now := logClock()
	l.writeEvent(now, l.eventLine(now, tag, "", msg), msg)
}

// LogDebug prints a debug line in grey (only when debug mode is on).
// 整行 MUTED：调试日志是次要信息，不该与正常运行日志抢注意力。
func (l *Logger) LogDebug(format string, args ...interface{}) {
	if !l.debug() {
		return
	}
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	now := logClock()
	l.writeEvent(now, l.eventLine(now, TagMuted, "", msg), msg)
}

// ---- 内容通道：不带时间戳、不带缩进 ----
//
// 控制台上有两类文字，混在一条通道上就是「输出乱」的根因：
//
//   - **事件**：过程（下载、重试、解析进度…），上面那组 Log* 方法打印，带时间戳；
//   - **内容**：这次运行的结果（标题、UP、分P 列表、流表格、直链、任务卡、汇总行），
//     由 Content* 打印——没有时间戳。它们不是「发生了一件事」，而是「结果就是这样」。
//
// 内容行以前走 LogColorNoTime：为了对齐日志前缀硬编码了 28 个空格，表格因此整段缩进、
// 与表头对不上。内容通道**不加任何缩进**，缩进由调用方按排版自己决定（见 download 的
// trackIndent / workflow 的 pageRow）。
//
// 与日志的一致性照旧：同一把 ConsoleLock + 「写之前先给进度条收尾」的 consoleWrite 约定
// （见上面的说明），否则卡片/表格会与原地重绘的进度行挤在同一行。

// Content 打一行内容（正文色，不额外设色）。参数按日志同款清洗：标题/UP 名等是服务端
// 可控文本，里面的换行与 ANSI 序列不能伪造出一行内容（上游 RF-54/RF-70）。
func (l *Logger) Content(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	l.contentText(msg+"\n", msg+"\n")
}

// ContentStyled 打一行**单段**样式的内容：整行一个角色（表头 MUTED、直链提示 MUTED…）。
func (l *Logger) ContentStyled(tag Tag, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	l.contentText(tag.Apply(msg)+"\n", msg+"\n")
}

// ContentLine 打一行**多段**样式的内容（信息行、表格行、段标题）。
//
// 各段文本按内容通道的约定清洗（服务端可控文本不得伪造换行/ANSI）；清洗只对脏文本生效，
// 排版用的空格原样保留，所以「无色版式」与「着色版式」逐字符一致。
func (l *Logger) ContentLine(line Line) {
	line = line.Sanitize()
	l.contentText(line.Render()+"\n", line.Plain()+"\n")
}

// ContentBlock 原样写出一段已排版好的**纯文本**：可多行、末尾换行与否由调用方决定，
// **不做**单行清洗（多行会被压成一行）。各字段的清洗由调用方负责。
func (l *Logger) ContentBlock(text string) { l.contentText(text, text) }

// ContentBlockStyled 写出一段多段样式的多行内容（任务卡）：每行末尾补一个换行。
func (l *Logger) ContentBlockStyled(block Block) {
	block = block.Sanitize()
	l.contentText(block.Render(), block.Plain())
}

// contentText 是内容通道的唯一出口：styled / plain 是同一段文本的两个视图（只差 SGR 序列），
// 能力允许时写着色那份，否则写纯文本那份——降级链的落点就在这里。
//
// 落盘永远是 plain + 全量时间戳：日志文件里没有终端上下文，色彩码只会污染 grep。
func (l *Logger) contentText(styled, plain string) {
	now := logClock()
	consoleWrite(func(w io.Writer) {
		if ColorsEnabled() {
			fmt.Fprint(w, styled)
			return
		}
		fmt.Fprint(w, plain)
	})
	// 日志文件里没有终端上下文，一行裸文本没法与事件对齐，所以补上全量时间戳；
	// 多行内容（任务卡）作为一段落下，不逐行补。
	l.appendToFile(filePrefix(now) + strings.TrimRight(plain, "\n"))
}

// Printf prints without timestamp prefix (for interactive prompts).
func (l *Logger) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	consoleWrite(func(w io.Writer) { fmt.Fprint(w, msg) })
}

// defaultIsTerminalOut 是终端判定的默认实现：stdout 是字符设备即视为终端。
//
// 与进度条（download.isTerminalOut / isTerminalStdout）同一口径，并集中在这里，避免各包
// 各养一份会漂移的判定。
func defaultIsTerminalOut() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// isTerminalOut 做成变量而不是直接调用：用例需要在不依赖真实终端的前提下驱动 TTY 分支
// （CI 的 stdout 永远是管道，真实判定恒为假，彩色/直链截断那两条路径就测不到）。
var isTerminalOut = defaultIsTerminalOut

// IsTerminalOut 把同一份判定给其它包用（直链渲染、进度条），口径只有一份。
func IsTerminalOut() bool { return isTerminalOut() }

// SetTerminalForTest 注入终端判定（fn 为 nil 表示还原），返回还原函数，调用方必须 defer 它。
func SetTerminalForTest(fn func() bool) (restore func()) {
	prev := isTerminalOut
	if fn == nil {
		isTerminalOut = defaultIsTerminalOut
	} else {
		isTerminalOut = fn
	}
	return func() { isTerminalOut = prev }
}

// Default package-level logger.
var defaultLogger = NewLogger(nil)

// SetLogFile 让默认 logger 同时把日志写入 path（追加）。
//
// 实现早就在 Logger.SetLogFile 里（含写失败自动挂起与恢复提示，对齐上游 AliverAnme 的文件日志），
// 但此前**没有任何 CLI 入口调用它**——功能存在却不可达。本包装 + --log-file 开关把它接上。
func SetLogFile(path string) { defaultLogger.SetLogFile(path) }

// SetDefaultDebugFn sets the debug callback for the default logger.
func SetDefaultDebugFn(fn func() bool) {
	defaultLogger.debugMode = fn
}

// DebugLogEnabled reports whether debug logging is currently enabled.
func DebugLogEnabled() bool {
	return defaultLogger.debugMode != nil && defaultLogger.debugMode()
}

// Log is the package-level convenience function.
func Log(format string, args ...interface{}) {
	defaultLogger.Log(format, args...)
}

// LogError is the package-level convenience function.
func LogError(format string, args ...interface{}) {
	defaultLogger.LogError(format, args...)
}

// LogWarn is the package-level convenience function.
func LogWarn(format string, args ...interface{}) {
	defaultLogger.LogWarn(format, args...)
}

// LogColor is the package-level convenience function.
func LogColor(format string, args ...interface{}) {
	defaultLogger.LogColor(format, args...)
}

// Content 是内容通道的包级入口（见 Logger.Content）：无时间戳、无缩进、正文色。
func Content(format string, args ...interface{}) {
	defaultLogger.Content(format, args...)
}

// ContentStyled 打一行单段样式的内容（表头 MUTED、提示 MUTED…），见 Logger.ContentStyled。
func ContentStyled(tag Tag, format string, args ...interface{}) {
	defaultLogger.ContentStyled(tag, format, args...)
}

// ContentLine 打一行多段样式的内容（信息行、表格行、段标题），见 Logger.ContentLine。
func ContentLine(line Line) { defaultLogger.ContentLine(line) }

// ContentBlock 原样写出一段已排版好的纯文本（可多行），见 Logger.ContentBlock。
func ContentBlock(text string) { defaultLogger.ContentBlock(text) }

// ContentBlockStyled 写出一段多段样式的多行内容（任务卡），见 Logger.ContentBlockStyled。
func ContentBlockStyled(block Block) { defaultLogger.ContentBlockStyled(block) }

// LogTagged 是事件通道的「自带状态列」入口（见 Logger.LogTagged）。
func LogTagged(tag Tag, format string, args ...interface{}) {
	defaultLogger.LogTagged(tag, format, args...)
}

// LogDebug is the package-level convenience function.
func LogDebug(format string, args ...interface{}) {
	defaultLogger.LogDebug(format, args...)
}

// GetLogger returns the default logger instance.
func GetLogger() *Logger {
	return defaultLogger
}
