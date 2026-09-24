package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// F4 多候选顺序回退：主地址之后的候选（替换前的原地址、playurl 的 backup_url）按序尝试，
// 首个 404 或连接失败就换下一个；换地址消耗的是同一条轨道级重试阶梯，不额外叠加请求。

// recordedServer 起一个假 CDN：记录 GET 顺序，按需回 404 或正常内容。
func recordedServer(t *testing.T, name string, notFound bool, body []byte, order *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			*order = append(*order, name)
			mu.Unlock()
		}
		if notFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func getOrder(order *[]string, mu *sync.Mutex) []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(*order))
	copy(out, *order)
	return out
}

// TestDownloadWalksCandidateChainOn404 三个候选依次 404，最后一个可用：必须按序走完整条链。
//
// 变异验证：把 FallbackURLs 只留第一个（退回「单次回退」语义），本用例变红（下载失败）。
func TestDownloadWalksCandidateChainOn404(t *testing.T) {
	body := []byte("0123456789abcdef")
	var order []string
	var mu sync.Mutex
	primary := recordedServer(t, "primary", true, body, &order, &mu)
	mirror2 := recordedServer(t, "mirror2", true, body, &order, &mu)
	origin := recordedServer(t, "origin", false, body, &order, &mu)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), RetryCount: 3, RetryDelayMs: 1,
		FallbackURLs: []string{mirror2.URL + "/a.m4s", origin.URL + "/a.m4s"},
	}
	if err := DownloadFile(context.Background(), primary.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("候选链上第三个地址可用时必须成功: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("产物不符（err=%v, %d 字节）", err, len(got))
	}
	if got := getOrder(&order, &mu); len(got) != 3 || got[0] != "primary" || got[1] != "mirror2" || got[2] != "origin" {
		t.Errorf("GET 顺序 = %v，期望按候选链顺序 [primary mirror2 origin]", got)
	}
}

// TestCandidateChainLongerThanRetryCountStillTriesEachCandidate：候选数超过 --retry-count 时
// 尝试次数取 max(retry-count, 候选数)，保证每个候选至少出场一次（默认梯度的"不多试"语义
// 只在没有候选时成立）。
//
// 变异验证：尝试次数退回 cfg.retryCount()，本用例变红（第三个候选永远到不了）。
func TestCandidateChainLongerThanRetryCountStillTriesEachCandidate(t *testing.T) {
	body := []byte("candidate-chain")
	var order []string
	var mu sync.Mutex
	primary := recordedServer(t, "primary", true, body, &order, &mu)
	mirror2 := recordedServer(t, "mirror2", true, body, &order, &mu)
	origin := recordedServer(t, "origin", false, body, &order, &mu)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), RetryCount: 2, RetryDelayMs: 1,
		FallbackURLs: []string{mirror2.URL + "/a.m4s", origin.URL + "/a.m4s"},
	}
	if err := DownloadFile(context.Background(), primary.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("retryCount=2 但候选有 3 个时仍应把每个候选试一遍: %v", err)
	}
	if got := getOrder(&order, &mu); len(got) != 3 || got[2] != "origin" {
		t.Errorf("GET 顺序 = %v，期望第三个候选也出场", got)
	}
}

// TestDownloadAdvancesCandidateOnConnectionFailure：主地址连接被拒（不是 404）同样要换下一个
// 候选——镜像整体不可达时，在原地重试 retryCount 次没有任何意义。
//
// 变异验证：candidateAdvanceable 只认 404，本用例变红（连接失败后不再换地址）。
func TestDownloadAdvancesCandidateOnConnectionFailure(t *testing.T) {
	body := []byte("reached-origin")
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL + "/a.m4s"
	dead.Close() // 端口随即无人监听：连接被拒

	var order []string
	var mu sync.Mutex
	origin := recordedServer(t, "origin", false, body, &order, &mu)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), RetryCount: 2, RetryDelayMs: 1,
		FallbackURLs: []string{origin.URL + "/a.m4s"},
	}
	if err := DownloadFile(context.Background(), deadURL, dest, cfg); err != nil {
		t.Fatalf("主地址连接失败时应换到可用候选: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("产物不符（err=%v, %d 字节）", err, len(got))
	}
}

// TestDownloadKeepsCandidateOnNon404Status：服务器明确回了 500 时不换候选——换地址只会拿到
// 同样的应答，白白烧掉候选（阶梯语义仍是同地址重试）。
//
// 变异验证：candidateAdvanceable 改成「一律返回 true」，本用例变红（候选被无谓访问）。
func TestDownloadKeepsCandidateOnNon404Status(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	var order []string
	var mu sync.Mutex
	spare := recordedServer(t, "spare", false, []byte("ok"), &order, &mu)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), RetryCount: 2, RetryDelayMs: 1,
		FallbackURLs: []string{spare.URL + "/a.m4s"},
	}
	if err := DownloadFile(context.Background(), broken.URL+"/a.m4s", dest, cfg); err == nil {
		t.Fatal("500 时不得换候选后判成功")
	}
	if got := getOrder(&order, &mu); len(got) != 0 {
		t.Errorf("500 不该触发候选回退，实际访问了 %v", got)
	}
}

// TestMultiThreadWalksCandidateChainPerClip：多线程路径的每个分片独立走候选链（镜像 HEAD 正常
// 所以走了多线程，但 Range 请求恒 404）。
//
// 变异验证：去掉 downloadRange 里的 advanceCandidate 分支，本用例变红。
func TestMultiThreadWalksCandidateChainPerClip(t *testing.T) {
	body := fallbackBody()

	mirror1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirror1.Close()

	mirror2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirror2.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer origin.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{
		Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 3, RetryDelayMs: 1,
		FallbackURLs: []string{mirror2.URL + "/a.m4s", origin.URL + "/a.m4s"},
	}
	if err := DownloadFile(context.Background(), mirror1.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("分片应沿候选链回退到可用地址: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("产物不符（err=%v, %d 字节，期望 %d）", err, len(got), len(body))
	}
}
