package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// O7：serve 的执行并发闸门（默认 --max-concurrent 3）。闸门此前内联在 processTask 里，只能靠
// 真实网络任务才走得到——没有用例守「并发不超过上限」这条不变量。
//
// 判定只用**计数器**（峰值并发），墙钟只作参考日志：慢 runner 上时间断言会变成 flake。
//
// 变异验证：acquireSlot 改成无条件返回 true（等于没有闸门）→ 峰值并发超过上限，用例变红。
func TestExecutionGateCapsConcurrency(t *testing.T) {
	const (
		limit = 3
		tasks = 6
		hold  = 100 * time.Millisecond
	)
	s := NewAPIServer("http://127.0.0.1:0", limit, "", "")

	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < tasks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.acquireSlot(context.Background()) {
				t.Error("取槽失败：context 并没有取消")
				return
			}
			defer s.releaseSlot()
			cur := inFlight.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(hold)
			inFlight.Add(-1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	got := peak.Load()
	if got > limit {
		t.Errorf("峰值并发 %d 超过上限 %d：闸门失效", got, limit)
	}
	if got < 2 {
		t.Errorf("峰值并发只有 %d：闸门把并发压成了串行", got)
	}
	t.Logf("%d 个任务 / 上限 %d：峰值并发 %d，总时长 %v（理想 %v）", tasks, limit, got,
		elapsed.Round(time.Millisecond), time.Duration(tasks/limit)*hold)
}

// ctx 取消时取槽必须立刻返回 false：任务要能被取消掉，而不是卡在闸门口。
func TestAcquireSlotHonoursCancellation(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:0", 1, "", "")
	if !s.acquireSlot(context.Background()) {
		t.Fatal("首次取槽应当成功")
	}
	defer s.releaseSlot()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan bool, 1)
	go func() { done <- s.acquireSlot(ctx) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("已取消的 context 不该拿到槽")
		}
	case <-time.After(time.Second):
		t.Fatal("槽被占满且 context 已取消时，acquireSlot 必须立刻返回，不能挂住")
	}
}
