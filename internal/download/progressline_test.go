package download

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// captureStdout 把 os.Stdout 换成管道，收集 fn 期间的全部终端输出。
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

// withFakeTerminal 让进度条以为 stdout 是终端：CI 里 stdout 是管道，
// 真终端判定会让渲染器直接短路。两条判定都要换：
// isTerminalOut 决定「画不画进度条/选哪条进度路径」，isTerminalStdout 决定
// progressReader 起不起渲染协程（单线程路径的帧就是靠它印出来的）。
func withFakeTerminal(t *testing.T) {
	t.Helper()
	origOut := isTerminalOut
	origStdout := isTerminalStdout
	isTerminalOut = func() bool { return true }
	isTerminalStdout = func() bool { return true }
	t.Cleanup(func() {
		isTerminalOut = origOut
		isTerminalStdout = origStdout
		util.SetProgressLineActive(false)
	})
}

// TestProgressLineClearsItselfAndReleasesTheLine 断言收尾把整行擦干净，
// 并把这一行还给日志——留着最后一帧，紧接着的日志就会和它挤在同一行。
func TestProgressLineClearsItselfAndReleasesTheLine(t *testing.T) {
	withFakeTerminal(t)
	const frame = "[####------------------------------------]  10.00% |"
	out := captureStdout(t, func() {
		line := newProgressLine()
		line.draw(frame)
		line.clear()
		util.Log("合并分片...")
	})

	wantClear := "\r" + strings.Repeat(" ", len(frame)) + "\r"
	if !strings.Contains(out, wantClear) {
		t.Errorf("收尾没有擦掉整行，实际输出 %q", out)
	}
	if got := strings.Count(out, "\n"); got != 1 {
		t.Errorf("擦除后日志不该再补换行：换行数 %d，实际输出 %q", got, out)
	}
}

// TestLoggerFinishesProgressLineBeforeLogging 断言日志不会接在进度条后面：
// 进度条是原地重绘的，日志若与它同行，下一帧重绘会回到行首把这条日志整行擦掉。
func TestLoggerFinishesProgressLineBeforeLogging(t *testing.T) {
	withFakeTerminal(t)
	out := captureStdout(t, func() {
		line := newProgressLine()
		line.draw("[#####-----------------------------------]  12.50% |")
		util.Log("下载异常, 3s 后重试... (1/3)")
		util.Log("第二行")
	})

	// 第一条日志先把进度行收尾（一个换行），第二条不该再补
	if got := strings.Count(out, "\n"); got != 3 {
		t.Errorf("换行数 = %d，期望 3（1 次收尾 + 2 条日志），实际输出 %q", got, out)
	}
	if strings.Index(out, "下载异常") < strings.Index(out, "\r") {
		t.Errorf("日志出现在进度条之前，时序不对：%q", out)
	}
}
