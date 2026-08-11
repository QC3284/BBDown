package util

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger provides thread-safe, colored console logging with optional file output.
type Logger struct {
	mu          sync.Mutex
	logFilePath string
	debugMode   func() bool // callback to check if debug is enabled
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

func (l *Logger) appendToFile(line string) {
	l.mu.Lock()
	path := l.logFilePath
	l.mu.Unlock()
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func timestamp() string {
	return time.Now().Format("[2006-01-02 15:04:05.000]")
}

// Log prints a normal log line.
func (l *Logger) Log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	line := timestamp() + " - " + msg
	fmt.Println(line)
	l.appendToFile(line)
}

// LogError prints an error line in red.
func (l *Logger) LogError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	line := timestamp() + " - " + msg
	l.mu.Lock()
	fmt.Print(timestamp() + " - ")
	fmt.Print("\033[31m" + msg + "\033[0m\n")
	l.mu.Unlock()
	l.appendToFile(line)
}

// LogWarn prints a warning line in yellow.
func (l *Logger) LogWarn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	line := timestamp() + " - " + msg
	l.mu.Lock()
	fmt.Print(timestamp() + " - ")
	fmt.Print("\033[33m" + msg + "\033[0m\n")
	l.mu.Unlock()
	l.appendToFile(line)
}

// LogColorNoTime prints a colored line in cyan without timestamp, indented to align.
func (l *Logger) LogColorNoTime(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	fmt.Print("                            \033[36m" + msg + "\033[0m\n")
	l.mu.Unlock()
	l.appendToFile("                             " + msg)
}

// LogColor prints a colored line in cyan.
func (l *Logger) LogColor(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	line := timestamp() + " - " + msg
	l.mu.Lock()
	fmt.Print(timestamp() + " - ")
	fmt.Print("\033[36m" + msg + "\033[0m\n")
	l.mu.Unlock()
	l.appendToFile(line)
}

// LogDebug prints a debug line in grey (only when debug mode is on).
func (l *Logger) LogDebug(format string, args ...interface{}) {
	if l.debugMode == nil || !l.debugMode() {
		return
	}
	msg := fmt.Sprintf(format, args...)
	line := timestamp() + " - " + msg
	l.mu.Lock()
	fmt.Print("\033[90m" + line + "\033[0m\n")
	l.mu.Unlock()
	l.appendToFile(line)
}

// Printf prints without timestamp prefix (for interactive prompts).
func (l *Logger) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Print(msg)
}

// Default package-level logger.
var defaultLogger = NewLogger(nil)

// SetDefaultDebugFn sets the debug callback for the default logger.
func SetDefaultDebugFn(fn func() bool) {
	defaultLogger.debugMode = fn
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
