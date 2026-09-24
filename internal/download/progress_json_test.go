package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// F5 进度事件 JSON（--progress-json）。用例真的把每一行 json.Unmarshal 一遍：
// 「逐行可解析」是对外契约，查字符串包含证明不了它。

// withProgressJSON 打开 --progress-json，用例结束后恢复默认（关）。
func withProgressJSON(t *testing.T) {
	t.Helper()
	SetProgressJSON(true)
	t.Cleanup(func() { SetProgressJSON(false) })
}

// captureStderr 把 os.Stderr 换成管道，收集 fn 期间写进去的内容。
//
// progressJSONOut() 是**写入时**现取 os.Stderr，所以这个替换对事件同样生效：
// 用例验证的是真实的默认目标（stderr），而不是一个被替换掉的测试目标。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// captureProgressStreams 同时接管 stdout 与 stderr：JSON 必须只出现在 stderr，
// stdout 仍留给日志与产物路径——混进 JSON，外部集成就没法把 stdout 当数据流用了。
func captureProgressStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	stderr = captureStderr(t, func() { stdout = captureStdout(t, fn) })
	return stdout, stderr
}

// parseProgressEvents 逐行解析事件；任何一行解析失败即用例失败。
func parseProgressEvents(t *testing.T, out string) []progressEvent {
	t.Helper()
	var events []progressEvent
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev progressEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON（对外契约要求逐行可解析）: %v\n原文: %q", i+1, err, line)
		}
		events = append(events, ev)
	}
	return events
}

func serveMedia(t *testing.T, size int) *httptest.Server {
	t.Helper()
	body := bytes.Repeat([]byte("m"), size)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "media.bin", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestProgressJSONSingleThreadEmitsParsableEvents 覆盖单线程路径：--progress-json
// 打开时逐行 JSON 事件写 stderr、stdout 不被污染、末帧是 done 且计数完整。
//
// 变异验证：让 Read 只在终端下启动渲染协程（去掉 pr.json 那一支），或删掉 renderLoop
// 里的 JSON 分派，stderr 里就没有可解析事件，本用例变红。
func TestProgressJSONSingleThreadEmitsParsableEvents(t *testing.T) {
	withProgressJSON(t)
	const size = 2 << 20
	srv := serveMedia(t, size)

	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1}
	var err error
	stdout, stderr := captureProgressStreams(t, func() {
		err = DownloadFile(context.Background(), srv.URL+"/media.bin", filepath.Join(t.TempDir(), "out.bin"), cfg)
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	events := parseProgressEvents(t, stderr)
	if len(events) == 0 {
		t.Fatalf("--progress-json 打开后没有任何事件（stderr=%q）", stderr)
	}
	last := events[len(events)-1]
	if last.State != "done" {
		t.Errorf("末帧 state = %q，期望 done：消费方据此判断这次传输结束（事件=%+v）", last.State, last)
	}
	if last.Downloaded != size || last.Total != size {
		t.Errorf("末帧计数不完整: downloaded=%d total=%d，期望 %d", last.Downloaded, last.Total, size)
	}
	if last.Percent != 100 {
		t.Errorf("末帧 percent = %v，期望 100", last.Percent)
	}
	if strings.Contains(stdout, "\r") {
		t.Errorf("JSON 模式不该再重绘终端进度行: %q", stdout)
	}
	if strings.Contains(stdout, "percent") {
		t.Errorf("进度事件污染了 stdout（它要留给产物路径）: %q", stdout)
	}
}

// TestProgressJSONMultiThreadAggregatesEvents 覆盖多线程聚合路径：每一帧自洽
// （0<=percent<=100、downloaded<=total）、不回退，末帧 done 且计满。
//
// 变异验证：删掉 renderAggregateProgress 里的 JSON 分支（回到只画进度条），
// stderr 里没有事件，本用例变红。
func TestProgressJSONMultiThreadAggregatesEvents(t *testing.T) {
	withProgressJSON(t)
	const size = 3 << 20 // 1MB 分片 → 3 片，必然走聚合
	srv := serveMedia(t, size)

	cfg := DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 1}
	var err error
	stdout, stderr := captureProgressStreams(t, func() {
		err = DownloadFile(context.Background(), srv.URL+"/media.bin", filepath.Join(t.TempDir(), "out.bin"), cfg)
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	events := parseProgressEvents(t, stderr)
	if len(events) < 2 {
		t.Fatalf("多线程路径要发首帧与结束帧，实际 %d 条（stderr=%q）", len(events), stderr)
	}
	prev := int64(-1)
	for i, ev := range events {
		if ev.Total != size {
			t.Errorf("第 %d 帧 total=%d，期望 %d", i, ev.Total, size)
		}
		if ev.Downloaded < 0 || ev.Downloaded > ev.Total {
			t.Errorf("第 %d 帧 downloaded=%d 越界（total=%d）：进度事件给程序消费，不能自相矛盾", i, ev.Downloaded, ev.Total)
		}
		if ev.Percent < 0 || ev.Percent > 100 {
			t.Errorf("第 %d 帧 percent=%v 越界", i, ev.Percent)
		}
		if ev.Downloaded < prev {
			t.Errorf("第 %d 帧 downloaded 回退：%d → %d（本次没有分片重试）", i, prev, ev.Downloaded)
		}
		prev = ev.Downloaded
	}
	last := events[len(events)-1]
	if last.State != "done" || last.Downloaded != size || last.Percent != 100 {
		t.Errorf("末帧 = %+v，期望 done/100%%/%d 字节", last, size)
	}
	if strings.Contains(stdout, "") {
		t.Errorf("JSON 模式不该再重绘终端进度行: %q", stdout)
	}
}

// TestProgressJSONOffKeepsTerminalProgressBar 钉住默认行为：不传 --progress-json 时
// 一个 JSON 事件都没有，终端进度条照旧（本仓硬约束：关闭时行为与改前完全一致）。
//
// 变异验证：把 renderAggregateProgress 的 JSON 分支改成无条件走，或把开关默认置真，
// stderr 里会出现事件、stdout 里没有进度行，本用例变红。
func TestProgressJSONOffKeepsTerminalProgressBar(t *testing.T) {
	withFakeTerminal(t)
	const size = 3 << 20
	srv := serveMedia(t, size)

	cfg := DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 1}
	var err error
	stdout, stderr := captureProgressStreams(t, func() {
		err = DownloadFile(context.Background(), srv.URL+"/media.bin", filepath.Join(t.TempDir(), "out.bin"), cfg)
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if stderr != "" {
		t.Errorf("关闭 --progress-json 时不该有任何事件输出: %q", stderr)
	}
	if !strings.Contains(stdout, "") || !strings.Contains(stdout, "%") {
		t.Errorf("关闭时应当照旧画终端进度条: %q", stdout)
	}
}

// TestProgressJSONResumeReportsWholeFileProgress 钉住续传时的计数口径：
// 事件里的 downloaded 是「这个文件已完成多少」，不是「这一次读了多少」——
// 否则续传下载永远不会到 100%，按进度做自动化（比如到 100% 才混流）全错。
//
// 变异验证：去掉 downloader.go 单线程路径里的 pr2.base = offset，末帧 downloaded
// 只剩「补的那段缺口」，本用例变红。
func TestProgressJSONResumeReportsWholeFileProgress(t *testing.T) {
	const total = 2 << 20
	body := make([]byte, total)
	for i := range body {
		body[i] = byte(i % 251)
	}
	const cut = 512 << 10

	var mu sync.Mutex
	firstRun := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("ETag", "v1")
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(total))
			w.WriteHeader(http.StatusOK)
			return
		}
		rng := r.Header.Get("Range")
		if firstRun {
			// 第一次只发一半就断连：留下 .tmp 与匹配的续传清单。
			w.Header().Set("Content-Length", fmt.Sprint(cut))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body[:cut])
			firstRun = false
			panic(http.ErrAbortHandler)
		}
		start := int64(0)
		if strings.HasPrefix(rng, "bytes=") {
			_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), "%d", &start)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
		w.Header().Set("Content-Length", fmt.Sprint(total-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[start:])
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1, RetryDelayMs: 1}

	// 第一段（还不开 JSON）中途断连。
	if err := DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=1&sign=old&qn=80", dest, cfg); err == nil {
		t.Fatal("第一段应当失败（服务端中途断连）")
	}

	// 第二段开 JSON 续传。
	SetProgressJSON(true)
	t.Cleanup(func() { SetProgressJSON(false) })
	var err error
	_, stderr := captureProgressStreams(t, func() {
		err = DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=999&sign=new&qn=80", dest, cfg)
	})
	if err != nil {
		t.Fatalf("第二段应当续传成功: %v", err)
	}
	events := parseProgressEvents(t, stderr)
	if len(events) == 0 {
		t.Fatal("续传路径没有任何事件")
	}
	last := events[len(events)-1]
	if last.State != "done" || last.Downloaded != total || last.Total != total || last.Percent != 100 {
		t.Errorf("续传末帧 = %+v，期望 done/100%%/%d 字节（本次实际只补 %d 字节）", last, total, total-cut)
	}
}
