package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 这里钉住的是「接线」：两条下载路径都必须把「数据到达」打点给渲染循环
// （单线程 progressReader.Read、多线程 countingWriter → reportProgress），
// 否则 runProgressLoop 再实时也只会画首帧。节奏本身由 pacer_test.go 覆盖。

// TestProgressReaderDrawsOnDataArrival 断言单线程路径的进度帧跟着数据走：
// 第二次 Read 之后必须画出反映新字节数的帧，而不是停在首帧等定时器。
//
// 变异验证：删掉 progressReader.Read 里的 pacer.Signal()，输出里不会出现 20.00%，本用例变红。
func TestProgressReaderDrawsOnDataArrival(t *testing.T) {
	withFakeTerminal(t)
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, 100)),
			total:      100,
			lastTime:   time.Now(),
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		buf := make([]byte, 10)
		if _, err := pr.Read(buf); err != nil {
			t.Fatalf("Read: %v", err)
		}
		time.Sleep(2 * minFrameInterval) // 让首帧先落地，免得两帧被节流合并成一个
		if _, err := pr.Read(buf); err != nil {
			t.Fatalf("Read: %v", err)
		}
		time.Sleep(2 * minFrameInterval)
		pr.Close()
	})

	for _, want := range []string{"10.00%", "20.00%"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少反映数据到达的帧 %s：进度帧没有跟着数据走（输出 %q）", want, out)
		}
	}
}

// TestMultiThreadDownloadSignalsRendererOnDataArrival 断言多线程路径的同一件事：
// 渲染器拿到的 pacer 必须在下片数据写入后被唤醒，而不是靠定时器才发现「有新数据」。
//
// 变异验证：删掉 reportProgress 里的 pacer.Signal()，本用例变红。
func TestMultiThreadDownloadSignalsRendererOnDataArrival(t *testing.T) {
	orig := renderProgressBar
	t.Cleanup(func() { renderProgressBar = orig })
	woken := make(chan struct{}, 1)
	renderProgressBar = func(_ *atomic.Int64, _ int64, pacer progressPacer, done <-chan struct{}, stopped chan<- struct{}) {
		defer close(stopped)
		select {
		case <-pacer.Signals():
			woken <- struct{}{}
		case <-done:
			return
		}
		<-done
	}

	body := make([]byte, 3<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "media.bin", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()

	cfg := DownloadConfig{MultiThread: true, SegmentSizeMB: 1, RetryCount: 1, Client: newTestClient()}
	if err := DownloadFile(context.Background(), srv.URL+"/media.bin", filepath.Join(t.TempDir(), "out.bin"), cfg); err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	select {
	case <-woken:
	default:
		t.Error("多线程下载全程没有唤醒渲染器：reportProgress 没有把数据到达打点给 pacer")
	}
}

// TestAggregateProgressRedrawsOnSignals 驱动**真实的**多线程渲染器（不是替身渲染函数）：
// 信号到来时必须画出反映新字节数的帧。上面那条接线用例只证明「打点到了 pacer」，
// 这条证明渲染器确实消费了这些信号。
//
// 变异验证：把 renderAggregateProgress 里的 runProgressLoop 换成不消费信号的形式
// （或退回 125ms 定时器），32ms 窗口内不会出现 40.00% 的帧，本用例变红。
func TestAggregateProgressRedrawsOnSignals(t *testing.T) {
	withFakeTerminal(t)
	var counter atomic.Int64
	pacer := newProgressPacer()
	done := make(chan struct{})
	stopped := make(chan struct{})
	out := captureStdout(t, func() {
		go renderAggregateProgress(&counter, 100, pacer, done, stopped)
		time.Sleep(2 * minFrameInterval) // 等首帧（0.00%）落地
		counter.Store(40)
		pacer.Signal()
		time.Sleep(2 * minFrameInterval)
		close(done)
		<-stopped
	})

	for _, want := range []string{"0.00%", "40.00%"} {
		if !strings.Contains(out, want) {
			t.Errorf("聚合渲染器缺少帧 %s：没有跟着信号重绘（输出 %q）", want, out)
		}
	}
}
