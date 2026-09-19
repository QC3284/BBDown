package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// 上游 HttpUtilUserAgentTests：UA 的优先级是 显式参数 → 流配置（--user-agent）→ 进程级随机默认。
// 本仓的落点是 DownloadConfig.userAgent()：显式字段 → HTTPClient 上的 UA → 随机默认。

func TestUpstreamUserAgentPriority(t *testing.T) {
	// 显式参数优先
	if got := (DownloadConfig{UserAgent: "custom-ua"}).userAgent(); got != "custom-ua" {
		t.Errorf("显式 UA 未生效: %q", got)
	}

	// 未显式给出时取流配置（本仓写在 HTTPClient 上）
	client := util.NewHTTPClient(nil, func() string { return "" }, nil)
	client.SetUserAgent("flow-ua")
	if got := (DownloadConfig{Client: client}).userAgent(); got != "flow-ua" {
		t.Errorf("流配置 UA 未生效: %q", got)
	}

	// 显式覆盖流配置
	if got := (DownloadConfig{UserAgent: "custom-ua", Client: client}).userAgent(); got != "custom-ua" {
		t.Errorf("显式 UA 未覆盖流配置: %q", got)
	}

	// 都为空时回落到随机默认（上游断言非空且含 Mozilla/5.0）
	got := (DownloadConfig{}).userAgent()
	if got == "" || !strings.Contains(got, "Mozilla/5.0") {
		t.Errorf("默认 UA 应非空且含 Mozilla/5.0，实际 %q", got)
	}
}

// 端到端：探针/单线程/多线程三条下载路径都必须带上配置的 UA——
// 此前下载路径写死 "Mozilla/5.0"，--user-agent 只影响 API 请求。

func TestDownloadUsesConfiguredUserAgent(t *testing.T) {
	seen := make(chan string, 16)
	var body []byte
	for i := 0; i < 1<<20; i++ {
		body = append(body, byte(i))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("User-Agent")
		http.ServeContent(w, r, "f.bin", time.Time{}, strings.NewReader(string(body)))
	}))
	defer srv.Close()

	client := util.NewHTTPClient(nil, func() string { return "" }, nil)
	client.SetUserAgent("configured-ua")
	cfg := DownloadConfig{Client: client, MultiThread: true, SegmentSizeMB: 1, RetryCount: 1}

	if err := DownloadFile(context.Background(), srv.URL+"/f.bin", filepath.Join(t.TempDir(), "out.bin"), cfg); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	close(seen)
	n := 0
	for ua := range seen {
		n++
		if ua != "configured-ua" {
			t.Errorf("下载请求带的 UA = %q，期望 configured-ua", ua)
		}
	}
	if n == 0 {
		t.Error("没有观察到任何请求")
	}
}
