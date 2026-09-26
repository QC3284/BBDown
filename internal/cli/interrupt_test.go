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

	// regs 记录每次注册用的 channel。真实 signal 包会把同一个信号投递给**每一个注册**
	// （同一个 channel 重复注册还会重复投递），press 照此模拟：重复安装才会显形为
	// 「按一次 Ctrl+C = 计数两次 = 直接强退」。
	regs []chan<- os.Signal
}

func (s *interruptSpy) restore() { s.mu.Lock(); s.restores++; s.mu.Unlock() }

func (s *interruptSpy) exit(code int) {
	s.mu.Lock()
	s.exitCodes = append(s.exitCodes, code)
	s.mu.Unlock()
}

func (s *interruptSpy) notify(ch chan<- os.Signal) {
	s.mu.Lock()
	s.notified++
	s.regs = append(s.regs, ch)
	s.mu.Unlock()
}

func (s *interruptSpy) stop(_ chan<- os.Signal) { s.mu.Lock(); s.stopped++; s.mu.Unlock() }

// press 模拟一次 Ctrl+C：向每个注册投递一份信号。发送放在独立 goroutine 里（channel 容量 1，
// 消费方还没读时同步发会挂住用例），超时兜底避免残留 goroutine 卡死。
func (s *interruptSpy) press(sig os.Signal) {
	s.mu.Lock()
	regs := append([]chan<- os.Signal(nil), s.regs...)
	s.mu.Unlock()
	for _, ch := range regs {
		go func(ch chan<- os.Signal) {
			select {
			case ch <- sig:
			case <-time.After(2 * time.Second):
			}
		}(ch)
	}
}

// clearRootInterrupts 清掉进程级安装状态：用例共享同一个进程，上一条用例的安装不能漏进来。
func clearRootInterrupts() {
	rootInterrupts.mu.Lock()
	rootInterrupts.ctx, rootInterrupts.stop = nil, nil
	rootInterrupts.mu.Unlock()
}

func (s *interruptSpy) snapshot() (restores int, exitCodes []int, notified, stopped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restores, append([]int(nil), s.exitCodes...), s.notified, s.stopped
}

// installInterruptSpy 把四个注入点换成假实现，用例结束自动还原。
func installInterruptSpy(t *testing.T) *interruptSpy {
	t.Helper()
	clearRootInterrupts()
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
		clearRootInterrupts()
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

// TestDownloadTargetsInstallsInterruptHandler：没有根安装时（用例/其它直接入口），
// downloadTargets 必须自己装一份并在返回时注销——不装的话第一次 Ctrl+C 之后第二次会被
// 信号层吞掉，提示里的「再按一次 Ctrl+C 强制退出」就是空话；不注销的话会留给下一次下载。
//
// 变异验证：把 downloadTargets 里的 installInterrupts 去掉（改回用调用方的 ctx）→
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

// TestInstallInterruptsIsIdempotentAndStopsOnce：统一安装（Execute）只注册一次信号、
// 只注销一次；已经装过时再调用**不会**注册第二次——重复注册会让同一个 Ctrl+C 被投递多次，
// 计数直接跳到「第二次」，用户按一次就强退。
//
// 变异验证：去掉 installInterrupts 的复用分支（每次调用都 newInterruptContext）→
// notified 变 2、stopped 变 2，本用例变红。
func TestInstallInterruptsIsIdempotentAndStopsOnce(t *testing.T) {
	spy := installInterruptSpy(t)

	ctx, stop := installInterrupts(context.Background())
	if _, _, notified, stopped := spy.snapshot(); notified != 1 || stopped != 0 {
		t.Fatalf("首次安装应当注册一次信号且还没注销，实际 notify=%d stop=%d", notified, stopped)
	}

	// 第二次调用（子命令再要一次中断 ctx）必须复用，不注册新信号。
	got, stopAgain := installInterrupts(context.Background())
	if got != ctx {
		t.Error("重复安装应当复用同一个 ctx（取消语义只有一份）")
	}
	if _, _, notified, _ := spy.snapshot(); notified != 1 {
		t.Errorf("重复安装不得再注册信号（同一个 Ctrl+C 会被投递多次、计数错乱），实际 notify=%d", notified)
	}
	// 复用方拿到的 stop 是空操作：不能把根安装提前拆掉。
	stopAgain()
	if _, _, _, stopped := spy.snapshot(); stopped != 0 {
		t.Errorf("复用方不得注销根安装，实际 stop=%d", stopped)
	}

	stop()
	if _, _, _, stopped := spy.snapshot(); stopped != 1 {
		t.Errorf("安装者注销一次即可，实际 stop=%d", stopped)
	}
}

// TestRootInstallIsReusedByDownloadPath：Execute 统一安装之后，下载路径（downloadTargets）
// 不能再装第二份。否则同一个 Ctrl+C 要么被两份计数各算一次，要么在同一 channel 上收到两份
// ——「第一次 Ctrl+C」直接变成强退（130），清理路径（删半成品、登记未完成任务）全被跳过。
//
// 变异验证：去掉 installInterrupts 的复用分支 → notified=2、中断提示打印两次，本用例变红。
func TestRootInstallIsReusedByDownloadPath(t *testing.T) {
	spy := installInterruptSpy(t)

	ctx, stop := installInterrupts(context.Background())
	defer stop()
	if _, _, notified, stopped := spy.snapshot(); notified != 1 || stopped != 0 {
		t.Fatalf("统一安装应当注册一次信号且不注销，实际 notify=%d stop=%d", notified, stopped)
	}

	cmd := &cobra.Command{}
	cmd.SetContext(ctx) // Execute 交给根命令、cobra 再传给子命令的那一份
	captureStdout(t, func() {
		if err := downloadTargets(commandContext(cmd), cmd, config.DefaultMyOption(), nil, nil); err != nil {
			t.Fatalf("空目标列表应当无错误返回：%v", err)
		}
	})
	if _, _, notified, stopped := spy.snapshot(); notified != 1 || stopped != 0 {
		t.Fatalf("下载路径必须复用根安装（不再注册、也不得注销），实际 notify=%d stop=%d", notified, stopped)
	}

	// 一次 Ctrl+C 只算一次：优雅取消（提示一次）而不是强退。
	var pressOut string
	pressOut = captureStdout(t, func() {
		spy.press(os.Interrupt)
		waitFor(t, "第一次 Ctrl+C 应当取消共享 ctx", func() bool {
			select {
			case <-ctx.Done():
				return true
			default:
				return false
			}
		})
	})
	if got := strings.Count(pressOut, interruptNotice); got != 1 {
		t.Errorf("一次 Ctrl+C 只该提示一次（装了两份才会各提示一次），实际 %d 次：%q", got, pressOut)
	}
	if _, exits, _, _ := spy.snapshot(); len(exits) != 0 {
		t.Fatalf("第一次 Ctrl+C 不该强退（计数被重复安装打乱），实际退出码 %v", exits)
	}

	// 第二次才是强制退出：共享的那一份计数仍然有效。
	captureStdout(t, func() {
		spy.press(os.Interrupt)
		waitFor(t, "第二次 Ctrl+C 应当强制退出", func() bool {
			_, exits, _, _ := spy.snapshot()
			return len(exits) == 1
		})
	})
	_, exits, _, _ := spy.snapshot()
	if exits[0] != interruptExitCode {
		t.Errorf("第二次 Ctrl+C 的退出码应为 %d，实际 %d", interruptExitCode, exits[0])
	}
}

// TestInstallRootInterruptsReachesCommands：统一安装必须真的交到子命令手上——cobra 把根 ctx
// 传给被执行的子命令，子命令用 commandContext 取。漏掉这一步，子命令（watchlater/sub check/
// live/article/login/serve/doctor）还是拿不到「二次强制退出」，提示就是空话。
//
// 变异验证：去掉 installRootInterrupts 里的 rootCmd.SetContext(ctx) → 探针子命令拿到的是
// cobra 兜底的 Background（或 rootCmd.Context() 为 nil），本用例变红。
func TestInstallRootInterruptsReachesCommands(t *testing.T) {
	spy := installInterruptSpy(t)
	stop := installRootInterrupts()
	t.Cleanup(func() {
		stop()
		rootCmd.SetContext(nil)
	})

	rootCtx := rootCmd.Context()
	if rootCtx == nil {
		t.Fatal("统一安装应当把中断 ctx 交给根命令")
	}
	if _, _, notified, _ := spy.snapshot(); notified != 1 {
		t.Fatalf("统一安装应当只注册一次信号，实际 %d", notified)
	}

	var got context.Context
	probe := &cobra.Command{Use: "ctx-probe", Hidden: true, RunE: func(cmd *cobra.Command, _ []string) error {
		got = commandContext(cmd)
		return nil
	}}
	rootCmd.AddCommand(probe)
	t.Cleanup(func() {
		rootCmd.RemoveCommand(probe)
		rootCmd.SetArgs(nil)
	})
	rootCmd.SetArgs([]string{"ctx-probe"})
	if _, err := rootCmd.ExecuteC(); err != nil {
		t.Fatalf("探针子命令执行失败：%v", err)
	}
	if got == nil || got != rootCtx {
		t.Fatalf("子命令拿到的 ctx 不是统一安装的那一份（got=%v），中断语义覆盖不到它", got)
	}

	// 共享语义：第一次 Ctrl+C 取消子命令拿到的 ctx，且不强退。
	spy.press(os.Interrupt)
	waitFor(t, "子命令 ctx 应当随第一次 Ctrl+C 取消", func() bool {
		select {
		case <-got.Done():
			return true
		default:
			return false
		}
	})
	if _, exits, _, _ := spy.snapshot(); len(exits) != 0 {
		t.Errorf("第一次 Ctrl+C 不该强退，实际 %v", exits)
	}
}

// TestDoctorSubcommandUsesRootInterruptContext：只验证「探针命令拿到根 ctx」还不够——得有一条
// 真实子命令的用例。doctor 此前自己 signal.NotifyContext，改回旧写法时本用例变红（它拿到的
// 会是另一个 ctx，第二次 Ctrl+C 依旧被信号层吞掉）。
//
// 变异验证：把 doctor RunE 里的 commandContext(cmd) 改回 signal.NotifyContext(...) → 见到的
// ctx 与统一安装的那份不同，本用例变红。自检项用桩，全程不碰网络。
func TestDoctorSubcommandUsesRootInterruptContext(t *testing.T) {
	spy := installInterruptSpy(t)
	stop := installRootInterrupts()
	t.Cleanup(func() {
		stop()
		rootCmd.SetContext(nil)
		doctorCmd.SetContext(nil)
		rootCmd.SetArgs(nil)
	})

	orig := doctorChecks
	seenCh := make(chan context.Context, 1)
	doctorChecks = []func(context.Context, config.MyOption, *util.HTTPClient) doctorResult{
		func(ctx context.Context, _ config.MyOption, _ *util.HTTPClient) doctorResult {
			seenCh <- ctx
			return doctorResult{"桩自检项", "ok", "桩：一切正常"}
		},
	}
	t.Cleanup(func() { doctorChecks = orig })

	rootCmd.SetArgs([]string{"doctor"})
	captureStdout(t, func() {
		if _, err := rootCmd.ExecuteC(); err != nil {
			t.Fatalf("doctor 用桩自检项应当通过：%v", err)
		}
	})

	var seen context.Context
	select {
	case seen = <-seenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("doctor 的自检项没有被调用")
	}
	if seen != rootCmd.Context() {
		t.Fatalf("doctor 必须用统一安装的中断 ctx（实际 %v），否则第二次 Ctrl+C 还是会被吞", seen)
	}

	// 共享语义：第一次 Ctrl+C 取消这份 ctx，且不强退。
	spy.press(os.Interrupt)
	waitFor(t, "doctor 的 ctx 应当随第一次 Ctrl+C 取消", func() bool {
		select {
		case <-seen.Done():
			return true
		default:
			return false
		}
	})
	if _, exits, _, _ := spy.snapshot(); len(exits) != 0 {
		t.Errorf("第一次 Ctrl+C 不该强退，实际 %v", exits)
	}
}
