package download

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMultiThreadDownloadFinishesProgressLineBeforeLogging 钉住时序：
// 「合并分片...」这类日志必须等进度行擦干净之后再打。
//
// 不等的话，日志会与上一帧进度残影挤在同一行——用户实测看到的就是这一条：
// 一行是被日志覆盖掉左半边的 25.07% 残影，下一行才是完整的 100% 进度条，
// 看起来像「两个进度」。
func TestMultiThreadDownloadFinishesProgressLineBeforeLogging(t *testing.T) {
	orig := renderProgressBar
	t.Cleanup(func() { renderProgressBar = orig })
	renderProgressBar = func(_ *atomic.Int64, _ int64, done <-chan struct{}, stopped chan<- struct{}) {
		<-done
		// 模拟末帧擦除的耗时：不放慢的话，主协程即使不等也几乎总是先写日志，测不出时序。
		time.Sleep(100 * time.Millisecond)
		fmt.Print("PROGRESS-CLEARED\n")
		close(stopped)
	}

	body := make([]byte, 3<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "media.bin", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()

	cfg := DownloadConfig{MultiThread: true, SegmentSizeMB: 1, RetryCount: 1, Client: newTestClient()}
	var err error
	out := captureStdout(t, func() {
		err = DownloadFile(context.Background(), srv.URL+"/media.bin", filepath.Join(t.TempDir(), "out.bin"), cfg)
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	cleared := strings.Index(out, "PROGRESS-CLEARED")
	merged := strings.Index(out, "合并分片...")
	if cleared < 0 {
		t.Fatalf("进度行收尾标记未出现（下载流程没等它收尾就结束了）：%q", out)
	}
	if merged < 0 {
		t.Fatalf("输出缺少「合并分片...」：%q", out)
	}
	if cleared > merged {
		t.Errorf("先打了「合并分片...」才收尾进度行：日志会与进度残影挤在同一行（输出 %q）", out)
	}
}

// TestProgressReaderCloseWaitsForFinalFrame 断言单线程路径的同一时序：Close 返回时
// 进度行已经收尾，调用方才能安全地紧跟日志（上游用 using 作用域表达同一件事）。
func TestProgressReaderCloseWaitsForFinalFrame(t *testing.T) {
	withFakeTerminal(t)
	// 单核调度：这样「Close 是否真的等了」不取决于调度运气——
	// 不等的话渲染协程只能在主协程阻塞后才跑，收尾帧必然落在 AFTER-CLOSE 之后。
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, 64)),
			total:      64,
			lastTime:   time.Now(),
			isTerminal: true,
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, 16)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		pr.Close()
		// Close 返回即意味着进度行已经收尾（上游 using 作用域的语义）：
		// 渲染协程若还没跑完，调用方紧接着的日志就会与进度残影挤在同一行。
		select {
		case <-pr.finished:
		default:
			t.Error("Close 返回时渲染协程尚未收尾")
		}
	})

	if !strings.Contains(out, "\r") {
		t.Errorf("渲染协程没有画出任何一帧：%q", out)
	}
}

// TestAggregateProgressClearsLineOnFinish 驱动真实渲染器（不是接缝替身）：
// 结束后输出必须以「擦除帧」收尾，而不是把最后一帧留在屏幕上
// ——上游 ProgressBar.Dispose 调的就是 UpdateText(string.Empty)。
func TestAggregateProgressClearsLineOnFinish(t *testing.T) {
	withFakeTerminal(t)
	var counter atomic.Int64
	counter.Store(50) // 50%：帧内容长度确定，便于断言
	done := make(chan struct{})
	stopped := make(chan struct{})
	out := captureStdout(t, func() {
		go renderAggregateProgress(&counter, 100, done, stopped)
		close(done)
		<-stopped
	})

	if !strings.Contains(out, "50.00%") {
		t.Fatalf("没有画出进度帧：%q", out)
	}
	trimmed := strings.TrimSuffix(out, "\r")
	idx := strings.LastIndex(trimmed, "\r")
	if idx < 0 {
		t.Fatalf("收尾没有擦除帧：%q", out)
	}
	if strings.Trim(trimmed[idx+1:], " ") != "" {
		t.Errorf("收尾不是整行擦除：%q", trimmed[idx:])
	}
}
