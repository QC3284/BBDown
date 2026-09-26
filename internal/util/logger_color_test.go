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

// TestEventLineRolesAreGolden 钉住事件通道的角色分配与字面字节：
//
//	22:48:42  获取 aid 结束: 2     ← 时间戳 MUTED、消息 TEXT
//	22:48:42  ⚠ 风控拦截           ← 警告：⚠ + 警告色
//	22:48:42  ✗ 解析失败           ← 错误：✗ + 错误色
//	22:48:42  发现新版本 2.13.0    ← 通知：BRAND
//	22:48:42  调试行               ← 调试：整行 MUTED（合并成一段）
//
// 时间戳用注入时钟钉死（真实墙钟跑两次可能跨秒）。
//
// 变异验证：
//   - 把 MUTED 换成 TEXT（时间戳不着色）→ 每条断言红；
//   - 去掉 ⚠ / ✗ 标记 → 标记断言红；
//   - 把 LogError 的 TagError 换成 TagWarn → 错误行的色码断言红。
func TestEventLineRolesAreGolden(t *testing.T) {
	colorsOn256(t)
	pinLogClock(t)
	l := NewLogger(nil)                           // 默认档：时间戳只到时分秒
	dbg := NewLogger(func() bool { return true }) // --debug：全量时间戳，日志才打

	const (
		muted  = "\x1b[38;5;245m"
		brand  = "\x1b[38;5;45m"
		warn   = "\x1b[38;5;214m"
		errCol = "\x1b[38;5;203m"
		ts     = "19:19:41  "
		dbgTS  = "2026-09-26 19:19:41.967  "
	)
	cases := []struct {
		name string
		want string
		call func()
	}{
		{"Log", muted + ts + AnsiReset + "事件\n", func() { l.Log("事件") }},
		{"LogWarn", muted + ts + AnsiReset + warn + "⚠ 警告" + AnsiReset + "\n", func() { l.LogWarn("警告") }},
		{"LogError", muted + ts + AnsiReset + errCol + "✗ 失败" + AnsiReset + "\n", func() { l.LogError("失败") }},
		{"LogColor", muted + ts + AnsiReset + brand + "提示" + AnsiReset + "\n", func() { l.LogColor("提示") }},
		// 调试整行 MUTED（前后两段同标签，渲染时合并成一段）。
		{"LogDebug", muted + dbgTS + "调试" + AnsiReset + "\n", func() { dbg.LogDebug("调试") }},
	}
	for _, c := range cases {
		got := captureStdout(t, c.call)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}

	// 时间戳必须走 MUTED：它是次要信息，不该与正文同权重（改前整行同色）。
	out := captureStdout(t, func() { l.Log("正文") })
	if !strings.HasPrefix(out, muted) {
		t.Errorf("时间戳应当是 MUTED：%q", out)
	}
}

// TestEventColorUses16ColorFallback：256 色不可用时事件行落到 16 色档（错误 91 / 警告 93 /
// 通知 96 / 调试与时间戳 90），而不是打出字面的 "38;5;203"。
func TestEventColorUses16ColorFallback(t *testing.T) {
	colorsOn16(t)
	pinLogClock(t)
	l := NewLogger(nil)

	cases := []struct {
		name string
		want string
		call func()
	}{
		{"LogError", "\x1b[91m✗ 失败" + AnsiReset, func() { l.LogError("失败") }},
		{"LogWarn", "\x1b[93m⚠ 警告" + AnsiReset, func() { l.LogWarn("警告") }},
		{"LogColor", "\x1b[96m提示" + AnsiReset, func() { l.LogColor("提示") }},
		{"时间戳", "\x1b[90m19:19:41  " + AnsiReset, func() { l.Log("正文") }},
	}
	for _, c := range cases {
		if out := captureStdout(t, c.call); !strings.Contains(out, c.want) {
			t.Errorf("%s（16 色档）: 输出缺少 %q，实际 %q", c.name, c.want, out)
		}
	}
}

// TestEventsStayPlainWithoutColorCapability：非 TTY（管道）与 NO_COLOR / TERM=dumb 下
// 事件行不得出现任何 ANSI——改前 LogError/LogWarn/LogColor/LogDebug **无条件**打色码，
// 管道与日志里都留着转义序列。
//
// 变异验证：去掉各 Log* 的 Tag.Apply 能力判定（或直接拼 "\x1b[91m"）→ 本用例红。
func TestEventsStayPlainWithoutColorCapability(t *testing.T) {
	pinLogClock(t)
	l := NewLogger(nil)
	dbg := NewLogger(func() bool { return true }) // 调试行只在 --debug 下打

	cases := []struct {
		name string
		tty  bool
		env  ColorEnv
	}{
		{"非 TTY", false, ColorEnv{Term: "xterm-256color"}},
		{"NO_COLOR=1", true, ColorEnv{NoColor: true, Term: "xterm-256color"}},
		{"TERM=dumb", true, ColorEnv{Term: "dumb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreTerm := SetTerminalForTest(func() bool { return c.tty })
			restoreEnv := SetColorEnvForTest(func() ColorEnv { return c.env })
			defer func() { restoreEnv(); restoreTerm() }()

			out := captureStdout(t, func() {
				l.Log("正文")
				l.LogWarn("警告")
				l.LogError("失败")
				l.LogColor("提示")
				dbg.LogDebug("调试")
			})
			if strings.Contains(out, "\x1b") {
				t.Errorf("%s 下事件行不该有任何 ANSI：%q", c.name, out)
			}
			for _, want := range []string{
				"19:19:41  正文", "⚠ 警告", "✗ 失败", "提示", "2026-09-26 19:19:41.967  调试",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s 下缺少 %q：%q", c.name, want, out)
				}
			}
		})
	}
}

// TestLogFileStaysPlainUnderColor：日志**文件**永远是无色纯文本。
//
// 文件是事后对账/grep 用的：ANSI 序列落进文件既污染解析，也让文件与「事件对齐」的全量
// 时间戳前缀变得难读。着色只属于终端。
func TestLogFileStaysPlainUnderColor(t *testing.T) {
	colorsOn256(t)
	pinLogClock(t)
	path := t.TempDir() + "/run.log"
	l := NewLogger(nil)
	l.SetLogFile(path)

	captureStdout(t, func() {
		l.Log("正文")
		l.LogWarn("警告")
		l.LogError("失败")
	})
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读日志文件：%v", err)
	}
	if strings.Contains(string(body), "\x1b") {
		t.Errorf("日志文件不该带 ANSI：%q", string(body))
	}
	// 版式（含 ⚠ / ✗ 标记）与终端无色形态一致，文件里才能一眼扫出警告与错误。
	for _, want := range []string{"[2026-09-26 19:19:41.967] 正文", "⚠ 警告", "✗ 失败"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("日志文件缺少 %q：%q", want, string(body))
		}
	}
}
