package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// 412 风控：轮换自动 UA + 退避重试（本仓新增，见 docs/UPSTREAM_ALIGNMENT.md §4.40）。
//
// 变异验证：
//   - 去掉 rotateAutomaticUserAgent 的调用 → 三次 UA 相同，第一条用例变红；
//   - 去掉整个 412 分支 → 第一次 412 就抛错，第一条用例变红；
//   - rotateAutomaticUserAgent 里去掉 uaExplicit 判断 → 第二条用例变红。
func withFastRiskControlDelay(t *testing.T) {
	t.Helper()
	oldDelay := riskControlRetryDelay
	t.Cleanup(func() { riskControlRetryDelay = oldDelay })
	riskControlRetryDelay = time.Millisecond
}

func TestRiskControlRetriesWithRotatedAutomaticUserAgent(t *testing.T) {
	withFastRiskControlDelay(t)

	var mu sync.Mutex
	var uas []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uas = append(uas, r.Header.Get("User-Agent"))
		n := len(uas)
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	body, err := c.GetWebSource(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("前两次 412 后第三次应当成功：%v（UA 序列 %v）", err, uas)
	}
	if body != "ok" {
		t.Fatalf("body = %q，期望 ok", body)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(uas) != 3 {
		t.Fatalf("请求数 = %d，期望 3（1 次原始 + 2 次风控重试）", len(uas))
	}
	if uas[0] == uas[1] || uas[1] == uas[2] {
		t.Errorf("每次 412 后都应当换一个自动 UA：%v", uas)
	}
}

func TestRiskControlKeepsExplicitUserAgent(t *testing.T) {
	withFastRiskControlDelay(t)

	const explicit = "Mozilla/5.0 (custom) Chrome/999.0"
	var mu sync.Mutex
	var uas []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uas = append(uas, r.Header.Get("User-Agent"))
		mu.Unlock()
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()

	c := NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	c.SetUserAgent(explicit)
	if _, err := c.GetWebSource(context.Background(), srv.URL); err == nil {
		t.Fatal("一直 412 应当报错（不能无限重试）")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(uas) != riskControlMaxAttempts {
		t.Fatalf("尝试次数 = %d，期望 %d", len(uas), riskControlMaxAttempts)
	}
	for i, ua := range uas {
		if ua != explicit {
			t.Errorf("第 %d 次请求的 UA = %q，显式指定时不该被轮换", i+1, ua)
		}
	}
}

// 成功路径不该被拖进风控重试（回归：普通请求仍只发一次）。
func TestRiskControlLeavesSuccessfulRequestsAlone(t *testing.T) {
	withFastRiskControlDelay(t)

	var n int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	if _, err := c.GetWebSource(context.Background(), srv.URL); err != nil {
		t.Fatalf("普通请求应当成功：%v", err)
	}
	if n != 1 {
		t.Errorf("成功路径请求数 = %d，期望 1", n)
	}
}
