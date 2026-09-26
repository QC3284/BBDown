package download

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 续传的终端进度帧——接线级用例。progress_test.go 证明帧本身的口径（withBase/wholeTotal），
// 这里证明**真实下载路径**把 base=offset 接上了：此前出问题的正是接线（单线程的终端分支
// 忘了设 base，JSON 分支设了，同一次续传里两个百分比）。

// framePercentPattern 抓进度帧里的百分比（" 53.1%"：设计稿定了一位小数）。
var framePercentPattern = regexp.MustCompile(`([0-9]+\.[0-9])%`)

// newStreamCapture 接管 os.Stdout 并把写进去的内容累积起来，返回「读当前输出」的函数。
//
// 与 captureStdout 的区别：用例要在**中途**轮询输出（等到第一帧出现再放行服务端），
// 而不是等 fn 返回——这样就不必用固定 sleep 猜渲染协程什么时候跑。
func newStreamCapture(t *testing.T) func() string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	var (
		mu   sync.Mutex
		sb   strings.Builder
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				mu.Lock()
				sb.Write(buf[:n])
				mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		os.Stdout = old
		_ = w.Close()
		<-done
		_ = r.Close()
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return sb.String()
	}
}

// waitForFrame 轮询捕获到的输出，直到出现带百分比的进度帧，返回那一刻的快照。
// 判据是「帧已经画出来」这个可观测状态，不是固定 sleep。
func waitForFrame(t *testing.T, capture func() string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var seen string
	for time.Now().Before(deadline) {
		if seen = capture(); framePercentPattern.MatchString(seen) {
			return seen
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待进度帧超时（%v）：%q", timeout, seen)
	return ""
}

// maxFramePercent 取出捕获文本里出现过的最大百分比；没有帧即用例失败。
func maxFramePercent(t *testing.T, out string) float64 {
	t.Helper()
	max := -1.0
	for _, m := range framePercentPattern.FindAllStringSubmatch(out, -1) {
		if v, parseErr := strconv.ParseFloat(m[1], 64); parseErr == nil && v > max {
			max = v
		}
	}
	if max < 0 {
		t.Fatalf("捕获到的输出里没有百分比：%q", out)
	}
	return max
}

// TestResumeTerminalFrameCountsBaseBytes 是续传口径的接线级回归：真实下载第二次从
// 1 MiB（整份 2 MiB 的一半）处续传，终端进度帧必须按 (base+current)/(base+total) 出数。
//
// 服务端发完第一小块（8 KiB）就停住、等用例读完帧再放行：这一刻 current 不可能超过这一块，
// 帧里的百分比只可能来自「base 已计入」的口径（1 MiB/2 MiB → ≥50%，x/y 的分子 ≥1.0 MB）。
// 改前（终端分支没设 base，且 total 传的是整份长度）同一时刻是 0.39% 与 0.0/2.0 MB。
//
// 变异验证：删掉 singleDownload 里的 pr2.base = offset，或删掉 withBase 里的 base，
// 捕获到的帧退回 current/total，本用例红。
func TestResumeTerminalFrameCountsBaseBytes(t *testing.T) {
	const (
		total = 2 << 20
		cut   = 1 << 20 // 第一段留下 1 MiB：恰好一半
		head  = 8 << 10 // 第二段先发 8 KiB 就停住，等用例看完进度帧
	)
	body := make([]byte, total)
	for i := range body {
		body[i] = byte(i % 251)
	}

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	var firstRun atomic.Bool
	firstRun.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(total))
			w.WriteHeader(http.StatusOK)
			return
		}
		if firstRun.CompareAndSwap(true, false) {
			// 第一次只发一半就断连：留下 .tmp 与匹配的续传清单。
			w.Header().Set("Content-Length", fmt.Sprint(cut))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body[:cut])
			panic(http.ErrAbortHandler)
		}
		start := int64(0)
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), "%d", &start)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
		w.Header().Set("Content-Length", fmt.Sprint(total-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[start : start+head])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write(body[start+head:])
	}))
	t.Cleanup(srv.Close) // 阻塞的 handler 必须先放行：unblock 在它之后注册，先跑
	t.Cleanup(unblock)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1, RetryDelayMs: 1}
	// 第一段：中途断连，失败但留下 .tmp 与清单。这一段还没换假终端（CI 下 stdout 是管道，
	// 不产生进度帧），它的帧不会混进下面要断言的捕获里。
	if err := DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=1&sign=old&qn=80", dest, cfg); err == nil {
		t.Fatal("第一段应当失败（服务端中途断连）")
	}

	withFakeTerminal(t)
	capture := newStreamCapture(t)

	done := make(chan error, 1)
	go func() {
		done <- DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=999&sign=new&qn=80", dest, cfg)
	}()

	// 等到第一帧：此刻服务端被 release 挡住，current ≤ head。
	seen := waitForFrame(t, capture, 5*time.Second)
	unblock()
	if err := <-done; err != nil {
		t.Fatalf("第二段应当续传成功: %v", err)
	}
	if got, readErr := os.ReadFile(dest); readErr != nil || !bytes.Equal(got, body) {
		t.Fatalf("续传产物不完整或内容不符：err=%v 大小=%d", readErr, len(got))
	}

	if got := maxFramePercent(t, seen); got < 50 {
		t.Errorf("续传帧百分比 %v%% < 50%%：base 没有算进 pct（帧 %q）", got, seen)
	}
	if !strings.Contains(seen, "1.0/2.0 MB") {
		t.Errorf("续传帧的 x/y 没有含 base（期望 1.0/2.0 MB）：%q", seen)
	}
}
