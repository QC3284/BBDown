package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoggerSuspendsFileLoggingAfterRepeatedFailures: a full or read-only volume
// made every log line retry an open that cannot succeed.
func TestLoggerSuspendsFileLoggingAfterRepeatedFailures(t *testing.T) {
	l := NewLogger(nil)
	// Points into a directory that does not exist, so every open fails.
	l.SetLogFile(filepath.Join(t.TempDir(), "missing", "bbdown.log"))

	// The notice for the suspension goes to stderr; keep it out of the test output.
	oldStderr := os.Stderr
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		os.Stderr = devNull
		defer func() { os.Stderr = oldStderr; devNull.Close() }()
	}

	for i := 0; i < maxLogFailures; i++ {
		l.appendToFile("line")
	}

	l.mu.Lock()
	suspended := l.logSuspendedUntil
	l.mu.Unlock()
	if suspended.IsZero() {
		t.Fatal("repeated write failures must suspend file logging")
	}
	if !time.Now().Before(suspended) {
		t.Error("the suspension must cover a cooldown window in the future")
	}
}

// TestLoggerWritesAndResetsFailureCounter pins the working path.
func TestLoggerWritesAndResetsFailureCounter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bbdown.log")
	l := NewLogger(nil)
	l.SetLogFile(path)

	l.appendToFile("first")
	l.appendToFile("second")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file: %v", err)
	}
	if got := string(data); !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("log contents = %q", got)
	}
	l.mu.Lock()
	failures := l.logFailures
	suspended := l.logSuspendedUntil
	l.mu.Unlock()
	if failures != 0 || !suspended.IsZero() {
		t.Errorf("a successful write must clear the failure state (failures=%d suspended=%v)", failures, suspended)
	}
}
