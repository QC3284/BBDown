package workflow

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// 标准输入接缝（workflow.go 的 stdinReader）的用例。
//
// 纪律：接缝只经 stdinReaderValue/setStdinReader 访问。用例把接缝换回默认值（写）时，
// readIntSafe 的读可能还在另一个协程里（它就在那个协程里 Fscanf）——裸包级变量在这对
// 访问之间没有 happens-before 边，go test -race 实测报 DATA RACE。所以：
//   - 接缝本身是原子的（生产行为不变，只换同步方式）；
//   - 用例这边不再靠「50ms 之后大概已经阻塞了」的时序运气：blockingReader 进 Read 时
//     报信，cleanup 等到它才返回（见 TestReadIntSafeCancelled）。
// TestStdinReaderSeamIsConcurrencySafe 是「访问器必须原子」这条的变异验证靶子。

func TestReadIntSafeSuccess(t *testing.T) {
	old := stdinReaderValue()
	defer setStdinReader(old)
	setStdinReader(strings.NewReader("5\n"))
	v, ok := readIntSafe(context.Background())
	if !ok || v != 5 {
		t.Fatalf("readIntSafe = (%d, %v), want (5, true)", v, ok)
	}
}

func TestReadIntSafeInvalidInput(t *testing.T) {
	old := stdinReaderValue()
	defer setStdinReader(old)
	setStdinReader(strings.NewReader("not-a-number\n"))
	v, ok := readIntSafe(context.Background())
	// 非法输入保持既有行为: 返回 0 并继续(与 Fscanf 失败时 v 保持 0 一致)。
	if !ok || v != 0 {
		t.Fatalf("readIntSafe = (%d, %v), want (0, true)", v, ok)
	}
}

// TestReadIntSafeCancelled：阻塞型 reader 模拟用户一直不输入；取消后必须返回 ok=false。
//
// 与改前的两点差别（都是为了让「协程到底起跑没有」不再靠碰运气）：
//  1. 取消由「已经进入 Read」驱动（entered 信号），不再用固定 time.Sleep 等一个大概；
//  2. cleanup 在换回接缝**之前**先等 entered：用例返回时提示协程一定已经进过读，
//     不会把「读完之后被换掉」的时序不确定性留在测试里（泄漏的协程只阻塞在 Read 上，
//     不再碰接缝；可取消读是 ROADMAP 候选，不在本任务范围）。
func TestReadIntSafeCancelled(t *testing.T) {
	old := stdinReaderValue()
	entered := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Error("等不到 blockingReader 进入 Read：交互提示协程没有发起读")
		}
		setStdinReader(old)
	})
	setStdinReader(&blockingReader{entered: entered})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		v  int
		ok bool
	}
	done := make(chan result, 1)
	go func() {
		v, ok := readIntSafe(ctx)
		done <- result{v: v, ok: ok}
	}()

	// 等它确实阻塞在 Read 上再取消：这是「取消必须让 readIntSafe 返回 ok=false」的前提。
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等不到 blockingReader 进入 Read：readIntSafe 没有发起读")
	}
	cancel()
	select {
	case r := <-done:
		if r.ok {
			t.Fatal("cancelled readIntSafe should report ok=false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 readIntSafe 没有返回")
	}
}

// TestStdinReaderSeamIsConcurrencySafe 钉住接缝的并发安全：生产侧（交互提示协程）读、
// 用例侧换回默认值写，两者可能同时发生——这正是原始 DATA RACE 的形态。
//
// 变异验证：把 stdinReaderValue 改回裸读包级变量（不修其它地方）→ 本用例在 -race 下
// 必然报 DATA RACE（test 的写与 goroutine 的读之间没有任何 happens-before 边）。
func TestStdinReaderSeamIsConcurrencySafe(t *testing.T) {
	old := stdinReaderValue()
	defer setStdinReader(old)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// 四个读者持续读接缝（模拟提示协程），主协程持续换（模拟用例的 defer 换回）。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = stdinReaderValue()
			}
		}()
	}
	for i := 0; i < 200; i++ {
		setStdinReader(strings.NewReader("1\n"))
	}
	close(stop)
	wg.Wait()
}

// blockingReader 永远阻塞(模拟终端等待输入)，并在**进入** Read 时报信：
// entered 被 close（而不是发一个值），所以多处等待都能安全地等同一个信号。
type blockingReader struct {
	entered chan struct{}
	once    sync.Once
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
	}
	select {}
}

var _ io.Reader = (*blockingReader)(nil)
