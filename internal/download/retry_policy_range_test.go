package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

// 分片循环（downloadRange）与单线程共用同一条分类策略，但调用点各自独立——
// 这里钉住分片路径也真的按策略执行：确定性 4xx 立刻放弃、412 在 UA 无法轮换时立刻放弃。
//
// HEAD 必须成功（否则退化成单线程路径），所以两个假服务器都声明长度 + Accept-Ranges。

func rangeFailServer(t *testing.T, body []byte, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var gets atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		gets.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &gets
}

func TestMultiThreadHonorsRetryClassification(t *testing.T) {
	body := make([]byte, 3<<20) // 3MB / 1MB 分片 = 3 个分片

	t.Run("403 每个分片只试一次（确定性失败不重试）", func(t *testing.T) {
		srv, gets := rangeFailServer(t, body, http.StatusForbidden)
		cfg := DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 3, RetryDelayMs: 1}
		err := DownloadFile(context.Background(), srv.URL+"/v.m4s", filepath.Join(t.TempDir(), "out.bin"), cfg)
		if err == nil {
			t.Fatal("分片恒 403 必须报错")
		}
		// 旧行为每个分片重试满 3 次 = 9 次 GET。
		// 变异验证：去掉 downloadRange 里的「if !plan.Retry 就 return」，本断言变红（9 ≠ 3）。
		if got := gets.Load(); got != 3 {
			t.Errorf("3 个分片各发一次 GET，期望 3，实际 %d（确定性 4xx 不得重试）", got)
		}
	})

	t.Run("412 且显式 UA 无法轮换：每个分片也只试一次", func(t *testing.T) {
		srv, gets := rangeFailServer(t, body, http.StatusPreconditionFailed)
		client := newTestClient()
		client.SetUserAgent("explicit-ua")
		cfg := DownloadConfig{Client: client, MultiThread: true, SegmentSizeMB: 1, RetryCount: 3, RetryDelayMs: 1}
		err := DownloadFile(context.Background(), srv.URL+"/v.m4s", filepath.Join(t.TempDir(), "out.bin"), cfg)
		if err == nil {
			t.Fatal("分片恒 412 必须报错")
		}
		// 变异验证：去掉 downloadRange 里的 412 轮换门，本断言变红（3 分片 × 3 次 = 9）。
		if got := gets.Load(); got != 3 {
			t.Errorf("3 个分片各发一次 GET，期望 3，实际 %d（UA 无法轮换时不得重试）", got)
		}
	})
}
