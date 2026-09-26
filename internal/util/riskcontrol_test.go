package util

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 412 风控：轮换自动 UA + 退避重试（本仓新增，见 docs/UPSTREAM_ALIGNMENT.md §4.40）。
//
// 变异验证：
//   - 去掉 rotateAutomaticUserAgent 的调用 → 三次 UA 相同，第一条用例变红；
//   - 去掉整个 412 分支 → 第一次 412 就抛错，第一条用例变红；
//   - 去掉 rotateAutomaticUserAgent 里的 uaExplicit 判断 → 显式 UA 用例变红（UA 被换掉）；
//   - 去掉「换成功才重试」的门（!c.rotateAutomaticUserAgent() 那条 break）→ 显式 UA 用例变红（又回到 3 次尝试）；
//   - 退避不再翻倍（固定 1s）→ 退避翻倍用例变红。
func withFastRiskControlBackoff(t *testing.T) {
	t.Helper()
	oldBase, oldMax := riskControlBackoffBase, riskControlBackoffMax
	t.Cleanup(func() { riskControlBackoffBase, riskControlBackoffMax = oldBase, oldMax })
	riskControlBackoffBase = time.Millisecond
	riskControlBackoffMax = 8 * time.Millisecond
}

func TestRiskControlRetriesWithRotatedAutomaticUserAgent(t *testing.T) {
	withFastRiskControlBackoff(t)

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

// 任务 C：412 只在**成功轮换自动 UA** 之后才重试。显式 --user-agent 换不掉 UA，
// 带着同一个 UA 连打正是风控提示里劝阻的行为——第一次 412 就抛出。
//
// 期望从旧行为的 riskControlMaxAttempts（3 次）改为 1 次，是任务 C 的显式要求
// （「只在轮换 UA 成功后重试」），不是重试能力回退：UA 仍不被轮换、可操作提示照旧、
// 自动 UA 的用例仍是 3 次尝试。
//
// 变异验证：去掉 412 分支里的「轮换失败就 break」，本用例变红（尝试次数回到 3）。
func TestRiskControlKeepsExplicitUserAgentAndDoesNotRetry(t *testing.T) {
	withFastRiskControlBackoff(t)

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
	_, err := c.GetWebSource(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("一直 412 应当报错（不能无限重试）")
	}
	if !strings.Contains(err.Error(), "412") || !strings.Contains(err.Error(), "风控") {
		t.Errorf("412 应当带着可操作提示抛出，实际 %q", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(uas) != 1 {
		t.Fatalf("显式 UA 无法轮换时不得重试：尝试次数 = %d，期望 1", len(uas))
	}
	if uas[0] != explicit {
		t.Errorf("显式指定时 UA 不该被轮换：%q", uas[0])
	}
}

// 成功路径不该被拖进风控重试（回归：普通请求仍只发一次）。
func TestRiskControlLeavesSuccessfulRequestsAlone(t *testing.T) {
	withFastRiskControlBackoff(t)

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

// 412 的退避曲线：base 起每次翻倍、上限 max；0/负数按第 1 次算，base<=0 表示不退避。
//
// 变异验证：退避不再翻倍（恒为 base）→ 2s/4s/8s 各行变红。
func TestRiskControlBackoffDoublesAndCaps(t *testing.T) {
	cases := []struct {
		retry int
		want  time.Duration
	}{
		{-1, time.Second},
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 8 * time.Second},
		{100, 8 * time.Second},
	}
	for _, c := range cases {
		if got := RiskControlBackoff(c.retry, time.Second, 8*time.Second); got != c.want {
			t.Errorf("RiskControlBackoff(%d, 1s, 8s) = %v，期望 %v", c.retry, got, c.want)
		}
	}
	if got := RiskControlBackoff(3, time.Second, 0); got != 4*time.Second {
		t.Errorf("max<=0 表示不限：RiskControlBackoff(3, 1s, 0) = %v，期望 4s", got)
	}
	if got := RiskControlBackoff(3, 0, time.Second); got != 0 {
		t.Errorf("base<=0 表示不退避，实际 %v", got)
	}
}

// 重试前确实按曲线等待：两次 412 重试的日志里退避必须递增（1ms → 2ms）。
// 只读日志、不睡真实 1s，避免用例被墙钟绑住。
//
// 变异验证：调用点换成固定退避（如 riskControlBackoffBase）→ 第二次仍是 1ms，本用例变红。
func TestRiskControlRetriesUseGrowingBackoff(t *testing.T) {
	withFastRiskControlBackoff(t)

	var mu sync.Mutex
	var logs []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()

	c := NewHTTPClient(func() bool { return true }, func() string { return "" }, func(format string, args ...interface{}) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	if _, err := c.GetWebSource(context.Background(), srv.URL); err == nil {
		t.Fatal("恒 412 必须报错")
	}

	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	for _, want := range []string{"1ms 后重试", "2ms 后重试"} {
		if !strings.Contains(joined, want) {
			t.Errorf("风控退避应当逐次翻倍，日志里找不到 %q：\n%s", want, joined)
		}
	}
}
