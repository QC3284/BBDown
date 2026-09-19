package download

import "time"

// 进度帧节奏——有意偏离上游（见 docs/UPSTREAM_ALIGNMENT.md §4.31）。
//
// 上游 BBDown/Infrastructure/ProgressBar.cs 的重绘完全由定时器驱动：
// animationInterval = 1/8 秒，TimerHandler 画完再 ResetTimer() 续一次，
// 所以进度条每秒最多动 8 次，与「字节计数实时、重绘定时」的结构一致。
//
// 本仓把重绘改成数据驱动（用户要求：进度条改为实时）。代价是失去了定时器顺带提供的
// 两件东西——帧率上限，以及下载停滞时「转圈还在转」的活性提示，下面两个变量分别补回来。
var (
	// minFrameInterval 是两帧之间的最小间隔（≈60fps）。信号来自每个写入块
	// （countingWriter 每 32KB 一次、单线程路径每次 Read 一次，本地盘下载每秒可到
	// 数万次），不设下限会把终端写爆。
	minFrameInterval = 16 * time.Millisecond
	// idleHeartbeat 既是心跳周期、也是静默阈值：距上一帧超过它且期间没有数据帧时，
	// 补画一帧（进度不动，只推进转圈动画）——等价于上游定时器在停滞期间的重绘。
	idleHeartbeat = 125 * time.Millisecond
)

// progressPacer 把高频的「有新数据」事件合并成受节流的重绘信号。
//
// Signal 必须是非阻塞的：它从下载协程（每个写入块）同步调用，阻塞会直接把下载拖慢；
// 缓冲里已经有一个待处理信号时丢弃新的即可——重绘只需要知道「有新数据」。
//
// 用值类型而非指针：零值可用（signals 为 nil 时 Signal 是空操作、Signals 返回 nil），
// 所以用例里 &progressReader{...} 这样的字面量构造不会因为漏填 pacer 而炸。
type progressPacer struct {
	signals chan struct{}
}

func newProgressPacer() progressPacer {
	return progressPacer{signals: make(chan struct{}, 1)}
}

// Signal 报告「有新数据到达」，已有待处理信号时合并掉。
func (p progressPacer) Signal() {
	select {
	case p.signals <- struct{}{}:
	default:
	}
}

// Signals 暴露重绘信号源；零值 pacer 返回 nil（select 上永不就绪）。
func (p progressPacer) Signals() <-chan struct{} {
	return p.signals
}

// runProgressLoop 是两条下载路径共用的重绘循环：事件驱动 + 节流 + 静默心跳。
//
// 节奏：
//
//  1. 进入即画首帧——进度条在下载开始时立刻出现（上游要等第一个 125ms tick）；
//  2. signals 到达且距上一帧 ≥minFrameInterval 就立刻画；不足则用一次性 timer 补齐到
//     minFrameInterval 再画，窗口内的其余信号被合并——这就是「实时」与「不刷屏」的折中；
//  3. 距上一帧超过 idleHeartbeat（heartbeat 为真时）补画一帧，保留上游定时器在下载
//     停滞时提供的转圈提示；
//  4. done 到达：擦掉整行再返回（上游 ProgressBar.Dispose → UpdateText("")）。
//     调用方据 stopped/finished 通道保证这一步先于后续日志。
//
// render 只在调用它的这条协程里执行，闭包里的帧序号、速度状态无需加锁。
func runProgressLoop(signals <-chan struct{}, done <-chan struct{}, heartbeat bool, line *progressLine, render func()) {
	last := time.Now()
	var (
		delayTimer *time.Timer
		delayC     <-chan time.Time
	)
	defer func() {
		if delayTimer != nil {
			delayTimer.Stop()
		}
	}()
	var beatC <-chan time.Time
	if heartbeat {
		beat := time.NewTicker(idleHeartbeat)
		defer beat.Stop()
		beatC = beat.C
	}

	render() // 首帧：下载一开始进度条就在，而不是 125ms 后才出现
	for {
		select {
		case <-done:
			line.clear()
			return
		case <-signals:
			elapsed := time.Since(last)
			switch {
			case elapsed >= minFrameInterval:
				render()
				last = time.Now()
			case delayC == nil:
				// 距上一帧太近：延迟到最小间隔再画，窗口内的信号被合并掉。
				delayTimer = time.NewTimer(minFrameInterval - elapsed)
				delayC = delayTimer.C
			}
		case <-delayC:
			delayC = nil
			render()
			last = time.Now()
		case <-beatC:
			if time.Since(last) >= idleHeartbeat {
				render()
				last = time.Now()
			}
		}
	}
}
