package util

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout 把 os.Stdout 换成管道，收集 fn 打印的全部内容。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestLogColorsMatchUpstreamConsoleColors 把日志配色钉在上游 C# 使用的 .NET
// ConsoleColor 名字上。ConsoleColor.Red / Cyan / White 是**亮色**变体，对应
// SGR 9x；写成 31/36/37 会让错误行、提示行和横幅在每个终端上都呈现比上游更暗的
// 颜色，属于对用户可见的行为差异。
func TestLogColorsMatchUpstreamConsoleColors(t *testing.T) {
	l := NewLogger(func() bool { return true })

	cases := []struct {
		name string
		want string
		call func()
	}{
		{"LogError", "[91m", func() { l.LogError("err") }},  // ConsoleColor.Red
		{"LogWarn", "[33m", func() { l.LogWarn("warn") }},   // ConsoleColor.DarkYellow
		{"LogColor", "[96m", func() { l.LogColor("hint") }}, // ConsoleColor.Cyan
		{"LogDebug", "[90m", func() { l.LogDebug("dbg") }},  // ConsoleColor.DarkGray
	}
	for _, tc := range cases {
		out := captureStdout(t, tc.call)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: 输出缺少 %q，实际为 %q", tc.name, tc.want, out)
		}
		if !strings.Contains(out, AnsiReset) {
			t.Errorf("%s: 输出未复位颜色，实际为 %q", tc.name, out)
		}
	}

	// 内容通道的着色同样取 ConsoleColor.Cyan，但只在 TTY 生效——用例显式注入终端判定，
	// 否则 captureStdout 用的是管道，真实判定恒为假，这条断言就永远看不到颜色。
	restore := SetTerminalForTest(func() bool { return true })
	defer restore()
	out := captureStdout(t, func() { l.Content(ContentCyan, "hint") })
	if !strings.Contains(out, "[96m") || !strings.Contains(out, AnsiReset) {
		t.Errorf("ContentCyan（TTY）应输出 ConsoleColor.Cyan 并复位，实际 %q", out)
	}
}

// TestBannerColorsMatchUpstream 钉住 cmd/bbdown 横幅用的前景/背景色常量
// （ConsoleColor.White on ConsoleColor.DarkBlue）。
func TestBannerColorsMatchUpstream(t *testing.T) {
	if AnsiWhite != "[97m" {
		t.Errorf("横幅前景应对应 ConsoleColor.White(97)，实际 %q", AnsiWhite)
	}
	if AnsiBgDarkBlue != "[44m" {
		t.Errorf("横幅背景应对应 ConsoleColor.DarkBlue(44)，实际 %q", AnsiBgDarkBlue)
	}
}
