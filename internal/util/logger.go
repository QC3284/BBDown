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

// ANSI sequences mirroring the .NET ConsoleColor names the upstream C# uses.
//
// ConsoleColor.Red / Cyan / White are the *bright* variants of their colors, so they
// map to the 9x SGR codes; printing 31/36/37 would render errors, prompts and the
// banner darker than upstream on every terminal.
const (
	AnsiReset      = "\033[0m"
	AnsiBlack      = "\033[30m"  // ConsoleColor.Black      (二维码深色模块)
	AnsiRed        = "\033[91m"  // ConsoleColor.Red        (LogError)
	AnsiDarkYellow = "\033[33m"  // ConsoleColor.DarkYellow (LogWarn)
	AnsiCyan       = "\033[96m"  // ConsoleColor.Cyan       (LogColor / 提示符)
	AnsiDarkGray   = "\033[90m"  // ConsoleColor.DarkGray   (LogDebug)
	AnsiWhite      = "\033[97m"  // ConsoleColor.White      (横幅前景)
	AnsiBgDarkBlue = "\033[44m"  // ConsoleColor.DarkBlue   (横幅背景)
	AnsiBgRed      = "\033[101m" // ConsoleColor.Red       (失败输出底色，同样取亮色档)
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

// consoleOut 是日志的写入目标，默认 stdout。
//
// SetConsoleOutput 让 `--info-json` / `doctor --json` 这类**机读模式**把日志挪到 stderr：
// stdout 只留数据，管道（jq/脚本）才拿得到干净输入。清爽感的代价只是「日志换个流」。
var consoleOut io.Writer = os.Stdout

// SetConsoleOutput 切换日志输出流（nil 视为还原 stdout）。
func SetConsoleOutput(w io.Writer) {
	if w == nil {
		consoleOut = os.Stdout
		return
	}
	consoleOut = w
}

// progressLineActive 表示终端当前行上停着一条尚未收尾的进度条。
var progressLineActive atomic.Bool

// SetProgressLineActive 由进度条在绘制/擦除时告知日志：这一行现在归进度条。
func SetProgressLineActive(active bool) { progressLineActive.Store(active) }

// consoleWrite 在 ConsoleLock 之下执行一次完整的控制台写入。
//
// 写日志前先给进度条那一行收尾（换行）：进度条是原地重绘的，日志若直接接在它后面，
// 下一帧重绘会回到行首把这条日志整行擦掉。
func consoleWrite(write func()) {
	ConsoleLock.Lock()
	defer ConsoleLock.Unlock()
	if progressLineActive.Swap(false) {
		fmt.Fprint(consoleOut, "\n")
	}
	write()
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

func timestamp() string {
	return time.Now().Format("[2006-01-02 15:04:05.000]")
}

// Log prints a normal log line.
func (l *Logger) Log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	line := timestamp() + " - " + msg
	consoleWrite(func() { fmt.Fprintln(consoleOut, line) })
	l.appendToFile(line)
}

// LogError prints an error line in red.
func (l *Logger) LogError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	line := timestamp() + " - " + msg
	consoleWrite(func() {
		fmt.Fprint(consoleOut, timestamp()+" - ")
		fmt.Fprint(consoleOut, AnsiRed+msg+AnsiReset+"\n")
	})
	l.appendToFile(line)
}

// LogWarn prints a warning line in yellow.
func (l *Logger) LogWarn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	line := timestamp() + " - " + msg
	consoleWrite(func() {
		fmt.Fprint(consoleOut, timestamp()+" - ")
		fmt.Fprint(consoleOut, AnsiDarkYellow+msg+AnsiReset+"\n")
	})
	l.appendToFile(line)
}

// LogColorNoTime prints a colored line in cyan without timestamp, indented to align.
func (l *Logger) LogColorNoTime(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	consoleWrite(func() { fmt.Fprint(consoleOut, "                            "+AnsiCyan+msg+AnsiReset+"\n") })
	l.appendToFile("                             " + msg)
}

// LogColor prints a colored line in cyan.
func (l *Logger) LogColor(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	line := timestamp() + " - " + msg
	consoleWrite(func() {
		fmt.Fprint(consoleOut, timestamp()+" - ")
		fmt.Fprint(consoleOut, AnsiCyan+msg+AnsiReset+"\n")
	})
	l.appendToFile(line)
}

// LogDebug prints a debug line in grey (only when debug mode is on).
func (l *Logger) LogDebug(format string, args ...interface{}) {
	if l.debugMode == nil || !l.debugMode() {
		return
	}
	msg := fmt.Sprintf(format, sanitizeLogArgs(args)...)
	line := timestamp() + " - " + msg
	consoleWrite(func() { fmt.Fprint(consoleOut, AnsiDarkGray+line+AnsiReset+"\n") })
	l.appendToFile(line)
}

// Printf prints without timestamp prefix (for interactive prompts).
func (l *Logger) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	consoleWrite(func() { fmt.Fprint(consoleOut, msg) })
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

// LogColorNoTime prints colored text without timestamp prefix.
func LogColorNoTime(format string, args ...interface{}) {
	defaultLogger.LogColorNoTime(format, args...)
}

// LogDebug is the package-level convenience function.
func LogDebug(format string, args ...interface{}) {
	defaultLogger.LogDebug(format, args...)
}

// GetLogger returns the default logger instance.
func GetLogger() *Logger {
	return defaultLogger
}
