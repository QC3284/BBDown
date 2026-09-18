package util

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSanitizeLogString(t *testing.T) {
	// Legitimate text is preserved verbatim.
	for _, s := range []string{"【东方】红魔乡实况", "P1 1080P 高码率", "av12345", "zh-CN"} {
		if got := SanitizeLogString(s); got != s {
			t.Errorf("SanitizeLogString(%q) = %q, want unchanged", s, got)
		}
	}

	// A forged log line must collapse onto one line but keep its content.
	got := SanitizeLogString("标题\r\n[2026-01-01 00:00:00.000] - 伪造日志行")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("SanitizeLogString left a line break: %q", got)
	}
	if !strings.Contains(got, "标题") || !strings.Contains(got, "伪造日志行") {
		t.Errorf("SanitizeLogString dropped content: %q", got)
	}

	// ANSI escapes must not survive.
	if got := SanitizeLogString("title\x1b[31mRED"); strings.ContainsRune(got, 0x1b) {
		t.Errorf("SanitizeLogString left an escape byte: %q", got)
	}

	// Overlong values are bounded, counted in runes not bytes.
	long := strings.Repeat("あ", MaxLogFieldLen*2)
	if got := SanitizeLogString(long); len([]rune(got)) > MaxLogFieldLen+1 {
		t.Errorf("SanitizeLogString did not truncate: %d runes", len([]rune(got)))
	}

	if got := SanitizeLogString(""); got != "" {
		t.Errorf("SanitizeLogString(\"\") = %q, want empty", got)
	}
}

// TestLoggerCollapsesForgedLogLine is the end-to-end check of the sink: a
// server-controlled title used to be able to inject its own log lines and
// terminal escapes (upstream RF-54 / RF-70).
func TestLoggerCollapsesForgedLogLine(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	Log("直播间: %s (UP: %s)", "标题\r\n[伪造] 我是一整行假日志", "up\x1b[0m")

	_ = w.Close()
	os.Stdout = old

	out, _ := io.ReadAll(r)
	text := string(out)
	if strings.Count(text, "\n") != 1 {
		t.Errorf("a server-controlled value forged extra log lines:\n%q", text)
	}
	if strings.ContainsRune(text, 0x1b) {
		t.Errorf("an ANSI escape from a server-controlled value reached stdout:\n%q", text)
	}
	if !strings.Contains(text, "标题") {
		t.Errorf("the legitimate part of the value was lost:\n%q", text)
	}
}

func TestSanitizeLogArgKeepsTypes(t *testing.T) {
	e, ok := sanitizeLogArg(errors.New("boom\n伪造: 新行")).(error)
	if !ok {
		t.Fatal("an error argument must stay an error")
	}
	if strings.ContainsAny(e.Error(), "\r\n") {
		t.Errorf("error text still carries a line break: %q", e.Error())
	}
	if _, ok := sanitizeLogArg("plain").(string); !ok {
		t.Error("a string argument must stay a string")
	}
	if v := sanitizeLogArg(42); v != 42 {
		t.Errorf("a non-text argument was altered: %v", v)
	}
}
