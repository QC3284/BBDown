package download

import (
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
// 信号间隔取 1ms——比 minFrameInterval 小、又足够让消费者立刻跟上，这时发送侧缓冲合并不上
// 场，帧数上限只能由节流本身保证。
//
// 变异验证：撤掉 minFrameInterval 节流（信号到达即画），100 个信号画出约 100 帧，远超上界；
// 反向地，退回「125ms 定时器」时帧数只有两三帧，下界那一条会红。
func TestProgressLoopThrottlesFrameRate(t *testing.T) {
	pacer := newProgressPacer()
	frames, stop := startProgressLoop(t, pacer.Signals(), false)
	defer stop()
	waitFrames(t, frames, 1)

	const signals = 100
	start := time.Now()
	for i := 0; i < signals; i++ {
		pacer.Signal()
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(start)
	time.Sleep(2 * minFrameInterval) // 把在途的那一帧画完再数
	got := frames.Load() - 1         // 减去首帧

	// 上界随实际历时伸缩，避免慢 runner 上误判：节流下每秒最多 1/minFrameInterval 帧。
	maxFrames := int64(elapsed/minFrameInterval) + 3
	if got > maxFrames {
		t.Errorf("%d 个信号（间隔 1ms，历时 %v）画出 %d 帧，超过节流上界 %d：帧率节流没有生效",
			signals, elapsed.Round(time.Millisecond), got, maxFrames)
	}
	if got < 2 {
		t.Errorf("%d 个信号只画出 %d 帧：信号没有驱动重绘", signals, got)
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
