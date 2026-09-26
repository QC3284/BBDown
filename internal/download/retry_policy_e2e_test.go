package download

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 任务 C 的端到端部分：策略表（retry_policy_test.go）钉住「分类 → 决策」，
// 这里钉住两条下载循环真的按决策执行（轮换 UA、换候选、不重试）。

// 412 后必须**先轮换自动 UA** 再重试；换过 UA 之后成功下载。
//
// 变异验证：让 DownloadConfig.rotateUserAgentFor412 直接 return true 而不轮换
// → 两次 UA 相同，本用例变红。
func TestDownload412RotatesUserAgentThenRetries(t *testing.T) {
	old := riskControlBackoffBase
	riskControlBackoffBase = time.Millisecond // 有 UA 轮换才重试，退避本身不测时长
	t.Cleanup(func() { riskControlBackoffBase = old })

	body := []byte("rotated-ua-then-ok")
	var mu sync.Mutex
	var uas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		uas = append(uas, r.Header.Get("User-Agent"))
		n := len(uas)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.m4s")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 3, RetryDelayMs: 1}
	if err := DownloadFile(context.Background(), srv.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("412 后轮换 UA 应当重试成功: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(uas) != 2 {
		t.Fatalf("GET 次数 = %d，期望 2（1 次 412 + 1 次轮换 UA 后成功）：%v", len(uas), uas)
	}
	if uas[0] == uas[1] {
		t.Errorf("412 后必须先轮换自动 UA 再重试，两次 UA 都是 %q", uas[0])
	}
}

// 任务 C：412 只在轮换 UA 成功后重试。显式 UA 换不掉 → 第一次 412 就抛错，
// 不再带着同一个 UA 连打（旧行为是 3 次尝试，正是风控提示里劝阻的做法）。
//
// 变异验证：
//   - 去掉单线程循环里那条「NeedsUARotation 且轮换失败就 break」的分支 → GET=3，本用例变红；
//   - 去掉 rotateUserAgentFor412 里 c.UserAgent != "" 的判断 → 第二条子用例变红
//     （下载请求带的是 cfg.UserAgent，轮换 HTTPClient 上的 UA 根本影响不到它）。
func TestDownload412WithExplicitUserAgentDoesNotRetry(t *testing.T) {
	cases := []struct {
		name  string
		apply func(cfg *DownloadConfig)
	}{
		{"显式 UA 落在 HTTPClient 上", func(cfg *DownloadConfig) { cfg.Client.SetUserAgent("explicit-client-ua") }},
		{"下载配置里的显式 UA 优先于 HTTPClient", func(cfg *DownloadConfig) { cfg.UserAgent = "explicit-download-ua" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gets atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				gets.Add(1)
				w.WriteHeader(http.StatusPreconditionFailed)
			}))
			defer srv.Close()

			cfg := DownloadConfig{Client: newTestClient(), RetryCount: 3, RetryDelayMs: 1}
			c.apply(&cfg)
			err := DownloadFile(context.Background(), srv.URL+"/a.m4s", filepath.Join(t.TempDir(), "out.m4s"), cfg)
			if err == nil {
				t.Fatal("恒 412 必须报错")
			}
			if !strings.Contains(err.Error(), "412") {
				t.Errorf("错误应指出 412，实际 %q", err)
			}
			if got := gets.Load(); got != 1 {
				t.Errorf("显式 UA 无法轮换时不得重试：GET %d 次，期望 1", got)
			}
		})
	}
}

// 任务 C：4xx（非 412/404）是确定性失败，不重试——重试只会白烧预算并延误真实错误。
//
// 变异验证：把 classifyTrackError 的 400..499 分支改成 retryClassOther（旧行为一律重试）
// → GET=3，本用例变红。
func TestDownloadClientErrorFailsFastWithoutRetry(t *testing.T) {
	var gets atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		gets.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 3, RetryDelayMs: 1}
	err := DownloadFile(context.Background(), srv.URL+"/a.m4s", filepath.Join(t.TempDir(), "out.m4s"), cfg)
	if err == nil {
		t.Fatal("403 必须报错")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("错误应指出 403，实际 %q", err)
	}
	if got := gets.Load(); got != 1 {
		t.Errorf("4xx（非 412/404）是确定性失败，不应重试：GET %d 次，期望 1", got)
	}
}

// 任务 C：网络/传输错误有候选时**立刻**换地址，不为一个死地址退避。
// --retry-delay 设成 60s 作探针：一旦退避被执行，5s 的 ctx 会先到期，下载以 ctx.Err() 结束。
//
// 变异验证：把网络类在 candidates>1 时的 Backoff 改成 defaultBackoff（换候选仍发生但先睡满）
// → ctx 先到期，本用例变红。
func TestDownloadSwitchesCandidateImmediatelyOnNetworkError(t *testing.T) {
	body := []byte("candidate-reachable")
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL + "/a.m4s"
	dead.Close() // 端口随即无人监听：连接被拒

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer origin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), RetryCount: 2, RetryDelayMs: 60000,
		FallbackURLs: []string{origin.URL + "/a.m4s"},
	}
	if err := DownloadFile(ctx, deadURL, dest, cfg); err != nil {
		t.Fatalf("连接失败应立刻换候选（不得先睡满 --retry-delay）: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("候选产物不符（err=%v, %d 字节）", err, len(got))
	}
}

// 任务 C：网络错误没有候选时用**短退避**重试，而不是按 --retry-delay 长退避空转。
// --retry-delay 设成 60s 作探针：若走了长退避，3s 的 ctx 会先在 sleepCtx 里到期，
// 返回的就会是 context.DeadlineExceeded（而不是连接错误）。
//
// 变异验证：把 shortNetworkBackoff 换回 defaultBackoff → 本用例变红。
func TestDownloadNetworkErrorWithoutCandidateUsesShortBackoff(t *testing.T) {
	old := networkRetryBackoff
	networkRetryBackoff = time.Millisecond
	t.Cleanup(func() { networkRetryBackoff = old })

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL + "/a.m4s"
	dead.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 3, RetryDelayMs: 60000}
	err := DownloadFile(ctx, deadURL, filepath.Join(t.TempDir(), "out.m4s"), cfg)
	if err == nil {
		t.Fatal("连接恒失败必须报错")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("没有候选时应当用短退避重试，而不是长退避等到 ctx 超时: %v", err)
	}
}
