package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- t64：显示宽度工具（标题折行 / 事件行截断共用）----

// TestDisplayWidth 钉住宽度口径：ASCII 1、CJK/全角/emoji 2、组合记号与控制字符 0。
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 4},
		{"a中", 3},
		{"ﾊﾞ", 2},      // 半角片假名 + 浊点（组合记号，0 列）
		{"汉\u0301", 2}, // 汉字 + 组合重音
		{"ＡＢ", 4},      // 全角
		{"🙂", 2},       // emoji
		{"a\x01b", 2},  // 控制字符不占列
		{"字\r\n", 2},   // CR/LF 不占列（日志里不会出现，但口径要一致）
	}
	for _, c := range cases {
		if got := DisplayWidth(c.in); got != c.want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestTruncateDisplay 逐字钉住截断：放得下原样；超宽保留前 cols-1 列 + 「…」；
// cols=1 只剩省略号；cols<=0 或空串返回空串。
func TestTruncateDisplay(t *testing.T) {
	cases := []struct {
		in   string
		cols int
		want string
	}{
		{"abc", 10, "abc"},
		{"abcdefghij", 5, "abcd…"},
		{"汉汉汉", 5, "汉汉…"},
		{"汉汉", 1, "…"},
		{"abc", 0, ""},
		{"", 10, ""},
	}
	for _, c := range cases {
		if got := TruncateDisplay(c.in, c.cols); got != c.want {
			t.Errorf("TruncateDisplay(%q, %d) = %q, want %q", c.in, c.cols, got, c.want)
		}
	}
	// 截断后宽度不超过预算。
	if got := TruncateDisplay("汉字汉字汉字", 7); DisplayWidth(got) > 7 {
		t.Errorf("截断结果 %q 宽度 %d 超过 7", got, DisplayWidth(got))
	}
}

// TestWrapDisplay 逐字钉住折行：断点按显示宽度、只在整字符边界断开、**不丢字**
// （片段拼回去等于原文）；单个字符本身超宽时允许该行变宽（不丢信息）。
func TestWrapDisplay(t *testing.T) {
	// 4 个汉字 = 8 列，按 5 列折 → 每行 2 个汉字（4 列），第 3 个放不下。
	got := WrapDisplay("汉字汉字", 5)
	want := []string{"汉字", "汉字"}
	if len(got) != len(want) {
		t.Fatalf("WrapDisplay = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 行 = %q, want %q", i, got[i], want[i])
		}
	}
	// 不丢字：拼回去等于原文（中英混排 + 长串）。
	long := "《明日方舟：终末地》核心章节「丹青渡」版本PV — 4K HDR 试听"
	parts := WrapDisplay(long, 20)
	if strings.Join(parts, "") != long {
		t.Errorf("折行丢了字：%q 拼回 %q", parts, strings.Join(parts, ""))
	}
	for i, p := range parts {
		if DisplayWidth(p) > 20 {
			t.Errorf("第 %d 行 %q 宽度 %d 超过 20", i, p, DisplayWidth(p))
		}
	}
	// 单个超宽字符：允许该行变宽，但不丢字。
	if got := WrapDisplay("🙂", 1); len(got) != 1 || got[0] != "🙂" {
		t.Errorf("单个超宽字符不该丢：%q", got)
	}
	if got := WrapDisplay("", 10); got != nil {
		t.Errorf("空串应当返回 nil，实际 %q", got)
	}
	if got := WrapDisplay("abc", 0); got != nil {
		t.Errorf("cols<=0 应当返回 nil，实际 %q", got)
	}
}

// TestEventLinesTruncatedOnConsoleButFullInFile 钉住 t64 ② 的两条：
//   - 屏幕上的常规事件行按显示宽度截断 + 「…」（整行含 28 列时间戳 ≤ 终端宽）；
//   - 日志文件里仍是**全文**（排查靠它）。
//
// 变异验证：把 shortForConsole 换回原样返回 → 本用例红。
func TestEventLinesTruncatedOnConsoleButFullInFile(t *testing.T) {
	restore := SetTerminalWidthForTest(80)
	defer restore()
	// 截断只在交互终端发生：注入 TTY 判定（stdoutIsTerminal 是包级接缝）。
	origTTY := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	defer func() { stdoutIsTerminal = origTTY }()

	msg := "常规事件行" + strings.Repeat("汉", 60) + "结尾"
	var buf strings.Builder
	restoreLogs := RedirectConsoleLogs(&buf)
	Log("%s", msg)
	restoreLogs()

	line := strings.TrimRight(buf.String(), "\n")
	if !strings.HasSuffix(line, "…") {
		t.Errorf("超宽的事件行应当以「…」收尾：%q", line)
	}
	if w := DisplayWidth(line); w > 80 {
		t.Errorf("事件行（含时间戳）%d 列 > 80：%q", w, line)
	}
	if !strings.HasPrefix(line, "[") || !strings.Contains(line, "] - ") {
		t.Errorf("时间戳前缀不该被破坏：%q", line)
	}

	// 文件里是全文。
	logPath := filepath.Join(t.TempDir(), "bbdown.log")
	SetLogFile(logPath)
	defer SetLogFile("")
	buf.Reset()
	restoreLogs = RedirectConsoleLogs(&buf)
	Log("%s", msg)
	restoreLogs()
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), msg) {
		t.Errorf("日志文件应当写全文（%d 字），实际：%q", len(msg), string(logged))
	}
}

// TestEventLinesKeepFullTextWhenNotTerminal 钉住 TTY 判定那一层：管道 / 重定向（脚本、CI 日志）
// 没有终端列宽约束，事件行保留**全文**——截断只服务交互终端的可读性。
func TestEventLinesKeepFullTextWhenNotTerminal(t *testing.T) {
	origTTY := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return false }
	defer func() { stdoutIsTerminal = origTTY }()

	msg := "常规事件行" + strings.Repeat("汉", 60)
	var buf strings.Builder
	restoreLogs := RedirectConsoleLogs(&buf)
	Log("%s", msg)
	restoreLogs()
	if !strings.Contains(buf.String(), msg) {
		t.Errorf("非终端下不该截断：%q", buf.String())
	}
}
