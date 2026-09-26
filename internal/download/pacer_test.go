package download

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 事件驱动重绘的节奏用例（实现见 pacer.go）。
//
// 这里驱动的是真实的重绘循环 runProgressLoop：render 回调只记帧数，progressLine 传零值
// （enabled 为假，收尾擦行是空操作），断言落在「第几帧、什么时候出帧」上，不依赖终端。

// waitFrames 轮询等待帧数达到 want，超时即失败——不用固定 Sleep 等异步副作用。
func waitFrames(t *testing.T, frames *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for frames.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("等待第 %d 帧超时（当前 %d 帧）", want, frames.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// startProgressLoop 起一个只数帧的重绘循环，返回帧计数与停止函数。
func startProgressLoop(t *testing.T, signals <-chan struct{}, heartbeat bool) (*atomic.Int64, func()) {
	t.Helper()
	var frames atomic.Int64
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		runProgressLoop(signals, done, heartbeat, &progressLine{}, func() { frames.Add(1) })
	}()
	return &frames, func() {
		close(done)
		<-stopped
	}
}

// TestProgressLoopDrawsOnDataNotOnTimer 钉住「重绘由数据驱动」：没有数据到达时不该出帧，
// 数据一到就该立刻出帧。
//
// 变异验证：把 runProgressLoop 换回 125ms 定时器驱动（上游做法），安静窗口内会多出两帧以上，
// 本用例变红。
func TestProgressLoopDrawsOnDataNotOnTimer(t *testing.T) {
	pacer := newProgressPacer()
	frames, stop := startProgressLoop(t, pacer.Signals(), false)
	defer stop()

	waitFrames(t, frames, 1) // 首帧：下载一开始进度条就在
	// 三个上游动画周期（375ms）内没有任何数据：事件驱动只该有那一帧。
	time.Sleep(3 * (time.Second / 8))
	if got := frames.Load(); got != 1 {
		t.Errorf("无数据到达时画了 %d 帧（应只有首帧）：重绘又回到定时驱动了", got)
	}

	pacer.Signal()
	deadline := time.Now().Add(time.Second / 4)
	for frames.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := frames.Load(); got < 2 {
		t.Errorf("数据到达后 250ms 内没有重绘（当前 %d 帧）", got)
	}
}

// TestProgressLoopThrottlesFrameRate 钉住帧率上限：下载路径每个写入块都会打点（本地盘下载
// 每秒可到数万次），不能每个信号都画一帧把终端写爆。
//
// 时间源换成手动时钟（runProgressLoopWithClock + fakeProgressClock），帧节奏完全由用例推进，
// 判据是结构性的、与 runner 快慢无关：
//   - 信号到达时窗口没满：只排延迟 timer、一帧都不画；
//   - 同一窗口内再来信号：被合并（不排新 timer、不画帧）；
//   - 时钟推过一个窗口：恰好补一帧，且两次重绘的间隔 ≥ minFrameInterval 的 80%。
//
// 旧判据是「实测墙钟历时 / 节流间隔 + 3 的帧数上界」：历时由墙钟测、在途的那一帧会在窗口
// 边界补画，负载重（全仓并行 + 并发编译）时实测 100 信号 172ms 出 14 帧 > 上界 13 而偶发红。
// 现在帧数由注入时钟唯一决定，负载只影响「多久轮到用例」，不影响判据。
//
// 变异验证（M5/M6）：把节流门槛改成恒真（信号到达即画）或把 minFrameInterval 置 0，
// 信号到达当场就多出一帧，第一条断言即红；两次重绘的间隔也会掉到 0。
func TestProgressLoopThrottlesFrameRate(t *testing.T) {
	const rounds = 5
	// 20% 余量只吸收计时器实现差异；节流失效时两次重绘的间隔是 0。
	minGapOK := 8 * minFrameInterval / 10
	clock := newFakeProgressClock()
	pacer := newProgressPacer()
	var frames atomic.Int64
	var stamps []time.Time
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		runProgressLoopWithClock(clock, pacer.Signals(), done, false, &progressLine{}, func() {
			stamps = append(stamps, clock.Now())
			frames.Add(1)
		})
	}()
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(done); <-stopped }) }
	defer stop()

	waitFrames(t, &frames, 1) // 首帧：进入循环即画

	for round := 0; round < rounds; round++ {
		beforeFrames, beforeTimers := frames.Load(), clock.timerCount()
		// 窗口内的第一个信号：节流生效时必须只排一个延迟 timer，不画帧。
		pacer.Signal()
		waitSignalHandled(t, clock, &frames, beforeFrames, beforeTimers)
		if got := frames.Load(); got != beforeFrames {
			t.Fatalf("第 %d 轮：窗口未满就画了 %d 帧：节流没有生效", round+1, got-beforeFrames)
		}
		if got := clock.timerCount(); got != beforeTimers+1 {
			t.Fatalf("第 %d 轮：应当恰好排下 1 个延迟 timer，实际多了 %d 个", round+1, got-beforeTimers-1)
		}
		// 同一窗口内的第二个信号：必须被合并掉（不排新 timer、不画帧）。
		beforeCalls := clock.nowCalls()
		pacer.Signal()
		waitNowCalls(t, clock, beforeCalls+1)
		if got := frames.Load(); got != beforeFrames {
			t.Fatalf("第 %d 轮：窗口内的多余信号又画了 %d 帧：信号没有被合并", round+1, got-beforeFrames)
		}
		if got := clock.timerCount(); got != beforeTimers+1 {
			t.Fatalf("第 %d 轮：窗口内的多余信号又排了 %d 个 timer", round+1, got-beforeTimers-1)
		}
		// 推进时钟越过窗口：恰好补一帧。
		clock.advance(minFrameInterval)
		clock.fireDue()
		waitFrames(t, &frames, beforeFrames+1)
	}
	stop() // 等渲染协程退出后再读 stamps（通道关闭建立 happens-before）

	if got, want := frames.Load(), int64(1+rounds); got != want {
		t.Errorf("共画出 %d 帧，期望 %d（首帧 + 每轮 1 帧）：多余信号没有被节流合并", got, want)
	}
	for i := 1; i < len(stamps); i++ {
		if gap := stamps[i].Sub(stamps[i-1]); gap < minGapOK {
			t.Errorf("第 %d 与第 %d 次重绘间隔 %v < %v：同一个节流窗口里补画了第二帧",
				i, i+1, gap, minGapOK)
		}
	}
}

// TestProgressLoopHeartbeatKeepsSpinnerAlive 钉住停滞时的活性提示：没有数据到达（网络卡住、
// 正在等重试）时转圈仍要转——上游靠定时器顺带提供这个表现，改成事件驱动后由心跳补上。
func TestProgressLoopHeartbeatKeepsSpinnerAlive(t *testing.T) {
	frames, stop := startProgressLoop(t, nil, true) // 无信号源：只有心跳能出帧
	defer stop()
	waitFrames(t, frames, 1)

	deadline := time.Now().Add(4 * idleHeartbeat)
	for frames.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := frames.Load(); got < 2 {
		t.Errorf("停滞 %v 后仍只有 %d 帧：转圈停住，用户分不清下载是活着还是死了", 4*idleHeartbeat, got)
	}
}

// TestProgressPacerSignalNeverBlocks 钉住 Signal 的非阻塞语义：它从下载协程（每个写入块）
// 同步调用，一旦阻塞就会把下载拖慢。
func TestProgressPacerSignalNeverBlocks(t *testing.T) {
	pacer := newProgressPacer()
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for i := 0; i < 1000; i++ {
			pacer.Signal()
		}
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("1000 次 Signal 没有在 5s 内返回：Signal 是阻塞的，会拖慢下载")
	}

	pending := 0
	for {
		select {
		case <-pacer.Signals():
			pending++
			continue
		default:
		}
		break
	}
	if pending > 1 {
		t.Errorf("缓冲里积压了 %d 个信号（应合并为 1 个）", pending)
	}
}

// TestProgressPacerZeroValueIsInert 钉住零值可用：用例里的字面量构造（以及将来任何漏填 pacer
// 的结构体）退化成「不打点」，而不是 nil 指针崩溃。
func TestProgressPacerZeroValueIsInert(t *testing.T) {
	var pacer progressPacer
	pacer.Signal()
	if pacer.Signals() != nil {
		t.Error("零值 pacer 不应暴露信号通道")
	}
}

// TestProgressLoopCancelsPendingDelayOnImmediateRedraw 钉住「立即重绘要撤掉在途的延迟 timer」。
//
// 负载重时渲染协程可能被压住，等它回过神来下一个信号已经越过窗口（elapsed ≥ minFrameInterval），
// 于是它立即重绘；此时若在途的延迟 timer 不撤，那个 timer 随后还会响一次，在同一个节流窗口里
// 补画第二帧——这正是旧「帧数上界」用例在全仓并行负载下偶发红的原因（100 信号 172ms 出 14 帧）。
//
// 判据用注入时钟的可观测状态：立即重绘之后不应再留下任何「已排下、未触发」的延迟 timer；
// 再把到期 timer 都投递出去，给一个节流窗口的时间，帧数不得再涨。
//
// 变异验证：删掉 pacer.go 立即重绘分支里的 delayTimer.Stop()/delayTimer = nil/delayC = nil，
// 在途 timer 仍在，本用例两条判据同时红。
func TestProgressLoopCancelsPendingDelayOnImmediateRedraw(t *testing.T) {
	clock := newFakeProgressClock()
	pacer := newProgressPacer()
	var frames atomic.Int64
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		runProgressLoopWithClock(clock, pacer.Signals(), done, false, &progressLine{}, func() { frames.Add(1) })
	}()
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(done); <-stopped }) }
	defer stop()

	waitFrames(t, &frames, 1)
	// 窗口内的信号：只排延迟 timer，不画帧。
	pacer.Signal()
	waitSignalHandled(t, clock, &frames, 1, 0)
	if got := frames.Load(); got != 1 {
		t.Fatalf("窗口未满就画了 %d 帧", got-1)
	}
	// 时钟越过窗口但先不投递 timer：模拟「循环被压住，回头时已经过窗」。
	clock.advance(minFrameInterval)
	// 过窗后的信号：必须立即重绘，并撤掉在途 timer。
	beforeCalls := clock.nowCalls()
	pacer.Signal()
	waitNowCalls(t, clock, beforeCalls+1)
	waitFrames(t, &frames, 2)
	if got := clock.pendingTimers(); got != 0 {
		t.Errorf("立即重绘后仍有 %d 个在途延迟 timer：它随后会在同一节流窗口补画第二帧", got)
	}
	// 行为面：把到期的 timer 都投递出去，再给一个节流窗口的时间，帧数不得再涨。
	clock.fireDue()
	deadline := time.Now().Add(2 * minFrameInterval)
	for time.Now().Before(deadline) {
		if got := frames.Load(); got > 2 {
			t.Fatalf("立即重绘后同一窗口又补画了第 %d 帧", got)
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if got := frames.Load(); got != 2 {
		t.Errorf("共画出 %d 帧，期望 2（首帧 + 立即重绘 1 帧）", got)
	}
}

// ---- 手动时钟：让帧节奏完全由用例推进（见 TestProgressLoopThrottlesFrameRate） ----

// fakeProgressClock 是注入 runProgressLoopWithClock 的手动时钟：时间只由用例推进，
// 到期 timer 由用例显式投递。Now/NewTimer 的调用次数可观测——循环每收下一个信号都会先取
// 一次时间，所以「信号已经处理完」这个同步点不用靠 sleep 去猜调度。
type fakeProgressClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeProgressTimer
	calls  atomic.Int64
}

func newFakeProgressClock() *fakeProgressClock {
	return &fakeProgressClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeProgressClock) Now() time.Time {
	c.calls.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeProgressClock) NewTimer(d time.Duration) progressTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeProgressTimer{clock: c, deadline: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t
}

// NewTicker 只是满足接口：需要心跳的用例不走手动时钟。
func (c *fakeProgressClock) NewTicker(time.Duration) progressTicker { return fakeProgressTicker{} }

// advance 推进时钟；窗口是否已满由这个数决定，不再看墙钟。
func (c *fakeProgressClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fireDue 把到期且未投递、未被 Stop 的 timer 投递到它的通道（缓冲 1，不阻塞）。
func (c *fakeProgressClock) fireDue() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if !t.fired && !t.stopped && !t.deadline.After(c.now) {
			t.fired = true
			t.ch <- c.now
		}
	}
}

// pendingTimers 返回「已排下、未触发、未 Stop」的延迟 timer 数：立即重绘撤掉在途 timer 后
// 它必须归零，否则那个 timer 随后会在同一节流窗口补画第二帧。
func (c *fakeProgressClock) pendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.fired && !t.stopped {
			n++
		}
	}
	return n
}

// timerCount 返回已创建的 timer 数（含已停止/已触发的）。
func (c *fakeProgressClock) timerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// nowCalls 返回 Now() 的调用次数：循环每收下一个信号都会先取一次时间。
func (c *fakeProgressClock) nowCalls() int64 { return c.calls.Load() }

type fakeProgressTimer struct {
	clock    *fakeProgressClock
	deadline time.Time
	ch       chan time.Time
	fired    bool
	stopped  bool
}

func (t *fakeProgressTimer) C() <-chan time.Time { return t.ch }

// Stop 与真实 timer 同语义：撤掉之后不再投递（fireDue 跳过）。
func (t *fakeProgressTimer) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.stopped = true
}

// fakeProgressTicker 永不触发（心跳用例不走手动时钟）。
type fakeProgressTicker struct{}

func (fakeProgressTicker) C() <-chan time.Time { return nil }
func (fakeProgressTicker) Stop()               {}

// waitSignalHandled 等到循环处理完刚发出的信号：要么排下延迟 timer（节流生效），
// 要么当场补画了一帧（节流失效，交给调用方断言失败）。轮询可观测状态，不用固定 sleep。
func waitSignalHandled(t *testing.T, clock *fakeProgressClock, frames *atomic.Int64, beforeFrames int64, beforeTimers int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for clock.timerCount() <= beforeTimers && frames.Load() <= beforeFrames {
		if time.Now().After(deadline) {
			t.Fatalf("等循环处理信号超时：timer %d（期望 >%d）、帧 %d（期望 >%d）",
				clock.timerCount(), beforeTimers, frames.Load(), beforeFrames)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitNowCalls 等到循环取过第 want 次时间（每收下一个信号必取一次）。
func waitNowCalls(t *testing.T, clock *fakeProgressClock, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for clock.nowCalls() < want {
		if time.Now().After(deadline) {
			t.Fatalf("等循环处理信号超时：Now 调用 %d，期望 ≥%d", clock.nowCalls(), want)
		}
		time.Sleep(time.Millisecond)
	}
}
