package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// 镜像 404 → 回退替换前原地址（本仓有意差异，见 docs/UPSTREAM_ALIGNMENT.md §4.33）。
// 场景：--force-replace-host 默认开启，所有流都被改写到镜像；镜像不保证覆盖全部对象，
// 命中缺口时旧实现只会对同一个死地址重试满 3 次再整页重来。

func fallbackBody() []byte {
	body := make([]byte, 3<<20)
	for i := range body {
		body[i] = byte(i % 251)
	}
	return body
}

// TestDownloadFallsBackToOriginalHostOn404 单线程路径：目标恒 404，配置里带原地址 → 回退成功。
//
// 变异验证：删掉 downloadToFile 里的 cfg.fallbackURL 分支，本用例变红（下载失败）。
func TestDownloadFallsBackToOriginalHostOn404(t *testing.T) {
	var mirrorHits, originHits atomic.Int64
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirrorHits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirror.Close()

	body := []byte("0123456789abcdef")
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer origin.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 3, RetryDelayMs: 1, FallbackURL: origin.URL + "/a.m4s"}
	if err := DownloadFile(context.Background(), mirror.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("镜像 404 时应回退原地址并成功: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("回退后的产物不符（err=%v, %d 字节）", err, len(got))
	}
	if mirrorHits.Load() == 0 {
		t.Error("应当先试过镜像")
	}
	if originHits.Load() == 0 {
		t.Error("没有回退到原地址")
	}
}

// TestDownloadFallsBackPerClipOn404 多线程路径：镜像 HEAD 正常（所以走了多线程），
// 但分片 Range 请求恒 404 → 每个分片回退原地址。
//
// 变异验证：删掉 downloadRange 里的回退分支，本用例变红。
func TestDownloadFallsBackPerClipOn404(t *testing.T) {
	body := fallbackBody()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound) // 分片 Range 请求：镜像没有这个对象
	}))
	defer mirror.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer origin.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 2, RetryDelayMs: 1,
		FallbackURL: origin.URL + "/a.m4s",
	}
	if err := DownloadFile(context.Background(), mirror.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("分片 404 时应回退原地址并成功: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("回退后的产物不符（err=%v, %d 字节，期望 %d）", err, len(got), len(body))
	}
}

// 没有配置原地址时行为不变：404 仍然是失败（不会凭空多出一次请求）。
func TestDownloadWithoutFallbackStillFailsOn404(t *testing.T) {
	var hits atomic.Int64
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirror.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 2, RetryDelayMs: 1}
	if err := DownloadFile(context.Background(), mirror.URL+"/a.m4s", dest, cfg); err == nil {
		t.Fatal("没有原地址可回退时必须报错")
	}
}
