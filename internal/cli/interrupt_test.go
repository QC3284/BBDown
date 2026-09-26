package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/spf13/cobra"
)

// 下载路径的 Ctrl+C 收尾（对齐上游 v1.6.20 Program.Console_CancelKeyPress）：
//   - 第一次 → 提示 + 取消 ctx（等清理走完）；
//   - 第二次 → 强制退出（非 0）。
//
// 终端恢复 / 进程退出 / 信号注册都换成假实现：否则用例会改掉测试进程的终端状态、甚至把
// 测试进程退掉；真给自身发 SIGINT 在 Windows 上也不被支持（用例要跨平台跑）。

// interruptSpy 记录终端恢复、强制退出与信号注册/注销的调用。
type interruptSpy struct {
	mu        sync.Mutex
	restores  int
	exitCodes []int
	notified  int
	stopped   int
}

func (s *interruptSpy) restore() { s.mu.Lock(); s.restores++; s.mu.Unlock() }

func (s *interruptSpy) exit(code int) {
	s.mu.Lock()
	s.exitCodes = append(s.exitCodes, code)
	s.mu.Unlock()
}

func (s *interruptSpy) notify(_ chan<- os.Signal) { s.mu.Lock(); s.notified++; s.mu.Unlock() }

func (s *interruptSpy) stop(_ chan<- os.Signal) { s.mu.Lock(); s.stopped++; s.mu.Unlock() }

func (s *interruptSpy) snapshot() (restores int, exitCodes []int, notified, stopped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restores, append([]int(nil), s.exitCodes...), s.notified, s.stopped
}

// installInterruptSpy 把四个注入点换成假实现，用例结束自动还原。
func installInterruptSpy(t *testing.T) *interruptSpy {
	t.Helper()
	s := &interruptSpy{}
	origRestore, origExit := restoreTerminal, forceExit
	origNotify, origStop := notifyInterrupt, stopInterrupt
	restoreTerminal = s.restore
	forceExit = s.exit
	notifyInterrupt = s.notify
	stopInterrupt = s.stop
	t.Cleanup(func() {
		restoreTerminal, forceExit = origRestore, origExit
		notifyInterrupt, stopInterrupt = origNotify, origStop
	})
	return s
}

// waitFor 轮询一个可观测状态直到成立，超时即失败：不用固定 sleep 等异步副作用（慢 runner 上会变 flake）。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// TestCancelGateIsGracefulOnceThenForces 是「决定下一步动作」的纯函数：第一次/第二次的语义只在这里定义。
//
// 变异验证：把判定改成恒返回 cancelGraceful（或把门槛从 >1 改成 >0）→ 本用例变红。
func TestCancelGateIsGracefulOnceThenForces(t *testing.T) {
	gate := &cancelGate{}
	if got := gate.next(); got != cancelGraceful {
		t.Fatalf("第一次中断应当优雅取消（提示后等清理），实际 %v", got)
	}
	if got := gate.next(); got != cancelForce {
		t.Fatalf("第二次中断必须强制退出（清理可能卡在 ffmpeg/aria2c 上），实际 %v", got)
	}
	if got := gate.next(); got != cancelForce {
		t.Fatalf("第三次及以后仍应强制退出，实际 %v", got)
	}
}

// TestHandleInterruptFirstCancelsSecondExits 钉住两次中断的行为差异：
// 第一次取消 ctx（下载/混流才会走清理路径）且**不**退出；第二次强制退出且退出码非 0。
// 两次都要恢复终端（上游在判定之前就恢复，强制退出那条路也不会把终端留在彩色/隐藏光标状态）。
//
// 变异验证：删掉第一次分支里的 cancel() → 「第一次必须取消 ctx」变红；
// 删掉第二次的强制退出（恒优雅取消）→ 「第二次必须强制退出」变红。
func TestHandleInterruptFirstCancelsSecondExits(t *testing.T) {
	spy := installInterruptSpy(t)

	logPath := filepath.Join(t.TempDir(), "cancel.log")
	util.SetLogFile(logPath)
	t.Cleanup(func() { util.SetLogFile("") })

	cancels := 0
	cancel := func() { cancels++ }

	gate := &cancelGate{}
	var first, second cancelOutcome
	console := captureStdout(t, func() {
		first = handleInterrupt(gate, cancel)
		second = handleInterrupt(gate, cancel)
	})

	if first != cancelGraceful || second != cancelForce {
		t.Fatalf("第一次应优雅取消、第二次应强制退出，实际 %v / %v", first, second)
	}
	if cancels != 1 {
		t.Fatalf("第一次中断必须取消 ctx（否则下载停不下来），且只取消一次，实际 %d 次", cancels)
	}
	restores, exits, _, _ := spy.snapshot()
	if restores != 2 {
		t.Errorf("两次中断都要恢复终端（强制退出也要），实际 %d 次", restores)
	}
	if len(exits) != 1 {
		t.Fatalf("只有第二次中断该强制退出，实际退出码序列 %v", exits)
	}
	if exits[0] == 0 {
		t.Errorf("强制退出的退出码必须非 0（脚本据此判断被中断），实际 %d", exits[0])
	}
	if !strings.Contains(console, interruptNotice) || !strings.Contains(console, forceExitNotice) {
		t.Errorf("两次中断的提示都要打印，实际 %q", console)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("取消提示必须走日志（--log-file 要抓得到）：%v", err)
	}
	for _, want := range []string{interruptNotice, forceExitNotice} {
		if !strings.Contains(string(body), want) {
			t.Errorf("日志文件里缺少 %q：%q", want, string(body))
		}
	}
}

// TestRestoreTerminalResetsColorShowsCursorAndEndsProgressLine 走**真实**的终端恢复实现
// （不是假实现）：Console.ResetColor + Console.CursorVisible 的 ANSI 等价物必须写出来，
// 且在提示之前先给进度条那一行收尾——进度条是原地重绘的，不收尾的话「正在取消…」会接在残帧后面。
//
// 变异验证：删掉 restoreTerminalReal 里的换行 → 首字符断言变红。
func TestRestoreTerminalResetsColorShowsCursorAndEndsProgressLine(t *testing.T) {
	util.SetProgressLineActive(true) // 模拟「进度条正停在当前行」
	t.Cleanup(func() { util.SetProgressLineActive(false) })

	out := captureStdout(t, func() {
		handleInterrupt(&cancelGate{}, func() {})
	})
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("提示之前要先给进度条那一行收尾（换行）：%q", out)
	}
	if !strings.Contains(out, util.AnsiReset+showCursor) {
		t.Errorf("退出前要复位颜色并显示光标（Console.ResetColor + CursorVisible 的等价物）：%q", out)
	}
	if strings.Contains(out, "\n\n") {
		t.Errorf("换行只能补一次（补两次会在终端里留下空行）：%q", out)
	}
	if !strings.Contains(out, interruptNotice) {
		t.Errorf("第一次中断要提示正在取消：%q", out)
	}
}

// TestServeInterruptsCancelsOnFirstSignalAndStopsOnSecond 守的是信号循环本身：
// 它必须消费**第二次**信号（signal.NotifyContext 的旧行为是只处理第一次，第二次被信号层吞掉，
// 用户在清理卡住时按第二次毫无反应）。这里直接喂假信号，不依赖平台能否给自身发 SIGINT。
//
// 变异验证：让循环在第一次信号后 return（旧 NotifyContext 行为）→ 「第二次应当强制退出」等待超时变红。
func TestServeInterruptsCancelsOnFirstSignalAndStopsOnSecond(t *testing.T) {
	spy := installInterruptSpy(t)

	sig := make(chan os.Signal, 2)
	done := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(cancelled) }) }

	go serveInterrupts(sig, done, cancel)
	defer close(done)

	sig <- os.Interrupt
	waitFor(t, "第一次信号应当取消 ctx", func() bool {
		select {
		case <-cancelled:
			return true
		default:
			return false
		}
	})
	if _, exits, _, _ := spy.snapshot(); len(exits) != 0 {
		t.Fatalf("第一次信号不该强制退出，实际 %v", exits)
	}

	sig <- os.Interrupt
	waitFor(t, "第二次信号应当强制退出", func() bool {
		_, exits, _, _ := spy.snapshot()
		return len(exits) == 1
	})
	_, exits, _, _ := spy.snapshot()
	if exits[0] != interruptExitCode {
		t.Errorf("强制退出码应为 128+SIGINT=%d，实际 %d", interruptExitCode, exits[0])
	}
}

// TestDownloadTargetsInstallsInterruptHandler：runDownload 与 resume 共用 downloadTargets，
// 取消收尾必须装在**这条共用路径**上——不装的话第一次 Ctrl+C 之后第二次会被信号层吞掉，
// 提示里的「再按一次 Ctrl+C 强制退出」就是空话。
//
// 变异验证：把 downloadTargets 里的 newInterruptContext 去掉（改回用调用方的 ctx）→
// notified/stopped 都是 0，本用例变红。空目标列表让本用例不碰网络。
func TestDownloadTargetsInstallsInterruptHandler(t *testing.T) {
	spy := installInterruptSpy(t)

	if err := downloadTargets(context.Background(), &cobra.Command{}, config.DefaultMyOption(), nil, nil); err != nil {
		t.Fatalf("空目标列表应当无错误返回：%v", err)
	}
	_, _, notified, stopped := spy.snapshot()
	if notified != 1 {
		t.Errorf("downloadTargets 应当安装一次 Ctrl+C 处理（第一次取消 / 第二次强制退出），实际 %d", notified)
	}
	if stopped != 1 {
		t.Errorf("downloadTargets 返回时必须注销信号处理（不能留给同进程的下一次下载），实际 %d", stopped)
	}
}
