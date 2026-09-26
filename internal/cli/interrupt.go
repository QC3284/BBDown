package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/QC3284/BBDown/internal/util"
)

// 下载路径的 Ctrl+C 收尾（对齐上游 v1.6.20 Program.Console_CancelKeyPress）：
//   - 第一次：恢复终端 + 提示「正在取消并等待清理（再按一次 Ctrl+C 强制退出）」，取消 ctx，
//     让下载/混流走各自的清理路径（删半成品、登记未完成任务清单……）后再退出；
//   - 第二次：清理可能正卡在等外部进程（ffmpeg/aria2c）上，直接强制退出（非 0）。
//
// 为什么不继续用 signal.NotifyContext：它只在第一次信号时取消 ctx，之后信号仍由它自己接管，
// 第二次 Ctrl+C 会被**吞掉**——用户按了没反应，只能 kill -9。所以这里自己数信号次数。

const (
	// interruptExitCode 是强制退出的退出码：128+SIGINT（上游 Environment.Exit(130)）。
	// 非 0，脚本能据此判断进程是被中断的。
	interruptExitCode = 130

	// interruptNotice 是第一次 Ctrl+C 的提示（上游 Console_CancelKeyPress 的中文提示）。
	interruptNotice = "正在取消并等待清理（再按一次 Ctrl+C 强制退出）"

	// forceExitNotice 是第二次 Ctrl+C 的提示。
	forceExitNotice = "强制退出..."

	// showCursor 是 Console.CursorVisible = true 的 ANSI 等价物。
	showCursor = "\033[?25h"
)

// cancelOutcome 是「收到一次中断信号后做什么」的判定结果。
type cancelOutcome int

const (
	cancelGraceful cancelOutcome = iota // 首次：提示 + 取消 ctx，等清理完成
	cancelForce                         // 再次：强制退出
)

// cancelGate 记录收到过几次中断，决定下一步动作：第一次/第二次的语义只在这里定义。
type cancelGate struct{ count int }

func (g *cancelGate) next() cancelOutcome {
	g.count++
	if g.count > 1 {
		return cancelForce
	}
	return cancelGraceful
}

// restoreTerminal 与 forceExit 做成可注入的变量：用例换成记录调用的假实现——
// 否则一跑用例就会改掉测试进程的终端状态，甚至把测试进程退掉。
var (
	restoreTerminal = restoreTerminalReal
	forceExit       = os.Exit
)

// notifyInterrupt / stopInterrupt 包一层信号注册，便于用例在不真发 SIGINT 的情况下验证
// 「信号 → 判定」这条接线（Windows 上给自身发 os.Interrupt 不被支持，真发信号不可移植）。
var (
	notifyInterrupt = func(ch chan<- os.Signal) { signal.Notify(ch, os.Interrupt) }
	stopInterrupt   = func(ch chan<- os.Signal) { signal.Stop(ch) }
)

// restoreTerminalReal 是上游 Console.ResetColor + Console.CursorVisible = true 的 Go 等价物，
// 同时给进度条那一行收尾（换行）：进度条是原地重绘的，不收尾的话 shell 提示符会接在残帧后面。
//
// 收尾由这里负责，所以顺手清掉 util 的「进度行还在」标记——否则紧接着的日志会再补一个空行。
func restoreTerminalReal() {
	util.ConsoleLock.Lock()
	defer util.ConsoleLock.Unlock()
	util.SetProgressLineActive(false)
	fmt.Print("\n" + util.AnsiReset + showCursor)
}

// handleInterrupt 处理一次中断信号：先恢复终端，再按次数决定「优雅取消」还是「强制退出」。
// 返回本次判定，调用方据此决定是否继续等下一次信号。
func handleInterrupt(gate *cancelGate, cancel context.CancelFunc) cancelOutcome {
	restoreTerminal()
	action := gate.next()
	if action == cancelForce {
		util.LogWarn("%s", forceExitNotice)
		forceExit(interruptExitCode)
		return action
	}
	util.LogWarn("%s", interruptNotice)
	cancel()
	return action
}

// newInterruptContext 是下载入口（runDownload 与 resume，两者共用 downloadTargets）统一的取消入口：
// 返回的 CancelFunc 会注销信号处理并取消 ctx，可安全重复调用。
func newInterruptContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sig := make(chan os.Signal, 1)
	notifyInterrupt(sig)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			stopInterrupt(sig)
			close(done)
			cancel()
		})
	}
	go serveInterrupts(sig, done, cancel)
	return ctx, stop
}

// serveInterrupts 逐个消费中断信号并交给 handleInterrupt，直到停表（done）或强制退出。
func serveInterrupts(sig <-chan os.Signal, done <-chan struct{}, cancel context.CancelFunc) {
	gate := &cancelGate{}
	for {
		select {
		case <-done:
			return
		case <-sig:
			if handleInterrupt(gate, cancel) == cancelForce {
				return
			}
		}
	}
}
