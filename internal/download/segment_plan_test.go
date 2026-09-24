package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// O4：分片大小自适应。旧行为是固定 20MB——40MB 文件只切 2 片，而并发上限是 8，
// 中等大小文件白白浪费连接。未显式指定时按「分片数 ≈ 并发上限」倒推（1–20MB）。
//
// 变异验证：让 planSegmentBytes 恒返回 20MB，本用例第二段变红。
func TestPlanSegmentBytes(t *testing.T) {
	parallel := int64(maxConcurrentClips())

	// 显式值原样生效（既有用例与脚本都依赖 1MB 能精确切分）。
	for _, mb := range []int{1, 5, 7} {
		if got, want := planSegmentBytes(400<<20, DownloadConfig{SegmentSizeMB: mb}), int64(mb)<<20; got != want {
			t.Errorf("显式 %dMB：got %d want %d", mb, got, want)
		}
	}

	// 未指定 → 按并发数倒推：40MB 分成 ≈parallel 片。
	if got, want := planSegmentBytes(40<<20, DownloadConfig{}), (int64(40)<<20)/parallel; got != want {
		t.Errorf("40MB 自动分片 = %d，期望 %d（并发 %d）", got, want, parallel)
	}

	// 下界 1MB：小文件不切出上百片。
	if got := planSegmentBytes(3<<20, DownloadConfig{}); got != 1<<20 {
		t.Errorf("3MB 自动分片 = %d，期望下界 1MB", got)
	}
	// 上界 20MB：大文件分片不过多（与旧默认一致）。
	if got := planSegmentBytes(800<<20, DownloadConfig{}); got != 20<<20 {
		t.Errorf("800MB 自动分片 = %d，期望上界 20MB", got)
	}
}

// TestAdaptiveSegmentsBeatFixedSegmentsOnThrottledServer 用限速服务器量收益：
// 每条连接约 256KB/20ms（≈12.5MB/s），40MB 文件在固定 20MB（2 片）与自动（≈8 片）下的墙钟。
func TestAdaptiveSegmentsBeatFixedSegmentsOnThrottledServer(t *testing.T) {
	const total = 40 << 20
	body := make([]byte, total)
	for i := range body {
		body[i] = byte(i % 251)
	}

	var connections atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(total))
			w.WriteHeader(http.StatusOK)
			return
		}
		// http.ServeContent 处理 Range，但我们要限速：自己按 Range 切片写。
		start, end := int64(0), int64(total-1)
		status := http.StatusOK
		if rng := r.Header.Get("Range"); len(rng) > 6 {
			status = http.StatusPartialContent
			_, _ = fmt.Sscanf(rng[6:], "%d-%d", &start, &end)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		}
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(status)
		const chunk = 256 << 10
		for off := start; off <= end; off += chunk {
			hi := off + chunk
			if hi > end+1 {
				hi = end + 1
			}
			if _, err := w.Write(body[off:hi]); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	run := func(segMB int) (time.Duration, int64) {
		connections.Store(0)
		dest := filepath.Join(t.TempDir(), "out.bin")
		cfg := DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: segMB, RetryCount: 1}
		start := time.Now()
		if err := DownloadFile(context.Background(), srv.URL+"/v.mp4", dest, cfg); err != nil {
			t.Fatalf("下载失败（segMB=%d）: %v", segMB, err)
		}
		return time.Since(start), connections.Load()
	}

	fixed, fixedConns := run(20)
	adaptive, adaptiveConns := run(0)
	t.Logf("固定 20MB：%v（%d 条连接）｜自动：%v（%d 条连接）", fixed.Round(time.Millisecond), fixedConns,
		adaptive.Round(time.Millisecond), adaptiveConns)
	if adaptive >= fixed {
		t.Errorf("自动分片应当更快：固定=%v 自动=%v", fixed, adaptive)
	}
}
