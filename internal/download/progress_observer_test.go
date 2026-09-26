package download

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 任务 O：下载层的逐字节进度观察者（serve 的 SSE 数据源）。
//
// 契约在 progressobserver.go：ProgressEvent{Current,Total,SpeedBps} /
// WithProgressObserver(ctx, fn) / ProgressObserverFromContext(ctx)。观察者走 context，
// 不是包级变量；回调只在既有节流（runProgressLoop 的帧）之后发生。

// observerRecorder 收集观察者事件。回调来自进度渲染协程，读取发生在 DownloadFile
// 返回之后（Close 要等渲染协程收尾，通道关闭建立了 happens-before）；加锁只是为了让
// -race 下没有任何含糊。
type observerRecorder struct {
	mu     sync.Mutex
	events []ProgressEvent
}

func (r *observerRecorder) observer(ev ProgressEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *observerRecorder) snapshot() []ProgressEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ProgressEvent(nil), r.events...)
}

// TestProgressObserverSingleThreadReportsWholeFile 覆盖单线程路径（假 CDN）：
// 能收到事件、Current 单调不减、每个事件的 Total 都是整份文件长度、最终 Current==Total。
//
// 变异验证：去掉 progress.go 里 renderLoop / renderObserverLoop 的 notifyObserver 调用，
// 事件数为 0，本用例变红。
func TestProgressObserverSingleThreadReportsWholeFile(t *testing.T) {
	const size = 2 << 20
	srv := serveMedia(t, size)
	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1}
	if err := DownloadFile(ctx, srv.URL+"/media.bin", dest, cfg); err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	events := rec.snapshot()
	// 首帧（渲染循环进入即画）与收尾帧（循环结束后补发）各一条：两条回调缺一就会
	// 只剩 1 个事件，所以这里要求 ≥2 而不是 ≥1。
	if len(events) < 2 {
		t.Fatalf("单线程路径只收到 %d 个进度事件：观察者没接上（渲染循环没启动或回调被摘掉）", len(events))
	}
	prev := int64(-1)
	for i, ev := range events {
		if ev.Total != size {
			t.Errorf("第 %d 个事件 Total=%d，期望 %d（整份文件口径，含 base）", i, ev.Total, size)
		}
		if ev.Current < prev {
			t.Errorf("第 %d 个事件 Current 回退：%d → %d", i, prev, ev.Current)
		}
		if ev.Current < 0 || ev.Current > size {
			t.Errorf("第 %d 个事件 Current=%d 越界（total=%d）", i, ev.Current, size)
		}
		if ev.SpeedBps < 0 {
			t.Errorf("第 %d 个事件 SpeedBps=%v 为负", i, ev.SpeedBps)
		}
		prev = ev.Current
	}
	last := events[len(events)-1]
	if last.Current != size || last.Total != size {
		t.Errorf("末事件 = %+v，期望 Current==Total==%d：收尾帧保证消费方一定看到 100%%", last, size)
	}
	if got := fileSizeOrZero(dest); got != size {
		t.Errorf("产物长度 = %d，期望 %d", got, size)
	}
}

// TestProgressObserverMultiThreadAggregates 覆盖多线程分片聚合路径：同样能收到事件，
// Total 是整份文件长度，Current 不回退、不越过 Total，末事件计满。
//
// 越界那条同时钉住「分片完成后的补加 Add」不再作用于观察者：终端进度条为防帧回退会
// 重复累加（ROADMAP 记录的既有异常，末帧最多 +33%），SSE 消费者不能收到 Current>Total。
//
// 变异验证：去掉 downloader.go 多线程路径 renderProgressBar 调用里的 observer 传参
// （或删掉 renderAggregateProgress 的 notify），事件数为 0 / 末事件不完整，本用例变红。
func TestProgressObserverMultiThreadAggregates(t *testing.T) {
	const size = 3 << 20 // 1MB 分片 → 3 片，必然走聚合路径
	srv := serveMedia(t, size)
	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 1}
	if err := DownloadFile(ctx, srv.URL+"/media.bin", dest, cfg); err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	events := rec.snapshot()
	if len(events) < 2 {
		t.Fatalf("多线程路径要发首帧与收尾帧，实际 %d 个事件", len(events))
	}
	prev := int64(-1)
	for i, ev := range events {
		if ev.Total != size {
			t.Errorf("第 %d 个事件 Total=%d，期望 %d", i, ev.Total, size)
		}
		if ev.Current < 0 || ev.Current > ev.Total {
			t.Errorf("第 %d 个事件 Current=%d 越界（total=%d）：给程序读的数据不能自相矛盾", i, ev.Current, ev.Total)
		}
		if ev.Current < prev {
			t.Errorf("第 %d 个事件 Current 回退：%d → %d（本次没有分片重试）", i, prev, ev.Current)
		}
		prev = ev.Current
	}
	last := events[len(events)-1]
	if last.Current != size || last.Total != size {
		t.Errorf("末事件 = %+v，期望 Current==Total==%d", last, size)
	}
}

// TestProgressObserverAbsentKeepsDownloadUnchanged 钉住「没装观察者 = 与改前一致」：
//  1. 查询返回 nil（不是空闭包），调用方据此完全不进回调路径；
//  2. 观察者只跟着挂它的那个 ctx，不泄漏到父 ctx（用包级变量就会泄漏）；
//  3. 一次真实下载：非终端、无 --progress-json 时改前就没有任何进度输出，产物完整，
//     兄弟 ctx 上的观察者一个事件都收不到。
//
// 变异验证：把观察者实现成包级变量（全局可变状态），第 2/3 条变红——兄弟 ctx 的下载
// 会把事件送给别人的回调。
func TestProgressObserverAbsentKeepsDownloadUnchanged(t *testing.T) {
	if fn := ProgressObserverFromContext(context.Background()); fn != nil {
		t.Error("没有观察者时 ProgressObserverFromContext 应返回 nil")
	}
	if fn := ProgressObserverFromContext(WithProgressObserver(context.Background(), nil)); fn != nil {
		t.Error("WithProgressObserver(ctx, nil) 应当等于没挂")
	}

	rec := &observerRecorder{}
	parent := context.Background()
	child := WithProgressObserver(parent, rec.observer)
	if ProgressObserverFromContext(parent) != nil {
		t.Error("观察者泄漏回了父 ctx：这是包级全局状态")
	}
	if ProgressObserverFromContext(child) == nil {
		t.Fatal("子 ctx 取不到观察者")
	}

	const size = 2 << 20
	srv := serveMedia(t, size)
	dest := filepath.Join(t.TempDir(), "out.bin")
	var err error
	stdout, stderr := captureProgressStreams(t, func() {
		err = DownloadFile(parent, srv.URL+"/media.bin", dest, DownloadConfig{Client: newTestClient(), RetryCount: 1})
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("无观察者、非终端时下载不该有任何进度输出：stdout=%q stderr=%q", stdout, stderr)
	}
	if events := rec.snapshot(); len(events) != 0 {
		t.Errorf("事件落到了别的 ctx 的观察者上（包级全局状态）：%+v", events)
	}
	if got := fileSizeOrZero(dest); got != size {
		t.Errorf("产物长度 = %d，期望 %d", got, size)
	}
}

// TestProgressObserverDoesNotDisturbTerminalFrames 钉住「装了观察者也逐字节不改终端输出」：
// 同一帧数据下，观察者为 nil / 非 nil 两次渲染的 stdout 必须完全相同。
//
// 变异验证：让回调在 line.draw 之前改动帧数字（或持锁渲染），两次输出不再逐字节相等，
// 本用例变红。
func TestProgressObserverDoesNotDisturbTerminalFrames(t *testing.T) {
	withFakeTerminal(t)
	render := func(observer func(ProgressEvent)) string {
		var counter atomic.Int64
		counter.Store(50)
		done := make(chan struct{})
		stopped := make(chan struct{})
		return captureStdout(t, func() {
			go renderAggregateProgress(&counter, 100, newProgressPacer(), done, stopped, observer)
			// done 立即关闭：runProgressLoop 先画首帧再收尾，输出与调度时机无关。
			close(done)
			<-stopped
		})
	}
	rec := &observerRecorder{}
	without := render(nil)
	with := render(rec.observer)
	if without != with {
		t.Errorf("装了观察者后终端帧变了：\n没有观察者 %q\n有观察者   %q", without, with)
	}
	if len(rec.snapshot()) == 0 {
		t.Error("观察者一个事件都没收到（渲染循环没有回调）")
	}
}

// TestProgressObserverResumeStartsFromBase 钉住续传口径：与 --progress-json 一样，
// Current/Total 含 base。整份 2MiB、磁盘上已就位 512KiB，续传事件的最小 Current
// 必须 ≥ base（只报「本次补了多少」会让进度条从 0 重新开始），末事件 Current==Total。
//
// 变异验证：把 progress.go 的 withBase 换成裸 current（或 total 忘了加 base），
// 首事件 Current < base，本用例变红。
func TestProgressObserverResumeStartsFromBase(t *testing.T) {
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
			// 第一次只发一半就断连：留下 cut 字节的 .tmp 与匹配的续传清单。
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

	// 第一段中途断连（还不开观察者）。
	if err := DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=1&sign=old&qn=80", dest, cfg); err == nil {
		t.Fatal("第一段应当失败（服务端中途断连）")
	}

	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)
	if err := DownloadFile(ctx, srv.URL+"/v.mp4?deadline=999&sign=new&qn=80", dest, cfg); err != nil {
		t.Fatalf("第二段应当续传成功: %v", err)
	}

	events := rec.snapshot()
	if len(events) == 0 {
		t.Fatal("续传路径一个进度事件都没收到")
	}
	for i, ev := range events {
		if ev.Total != total {
			t.Errorf("第 %d 个事件 Total=%d，期望 %d（含 base 的整份文件）", i, ev.Total, total)
		}
		if ev.Current < cut {
			t.Errorf("第 %d 个事件 Current=%d < base=%d：续传口径没含 base", i, ev.Current, cut)
		}
	}
	last := events[len(events)-1]
	if last.Current != total || last.Total != total {
		t.Errorf("续传末事件 = %+v，期望 Current==Total==%d（本次实际只补 %d 字节）", last, total, total-cut)
	}
}

// TestProgressObserverIsThrottledToFrameRate 钉住「回调走在既有节流之后」：
// 16MiB 的假 CDN 会触发 512 次以上的 Read（io.Copy 每次 32KiB），若观察者挂在 Read 上
// 就会有几百个事件；帧节奏下每秒最多 1/minFrameInterval 帧（pacer_test.go 同一上界）。
//
// 变异验证：把 notifyObserver 挪进 progressReader.Read / countingWriter.Write（每条
// read 都回调），事件数远超上界，本用例变红。
func TestProgressObserverIsThrottledToFrameRate(t *testing.T) {
	const size = 16 << 20
	srv := serveMedia(t, size)
	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1}
	start := time.Now()
	if err := DownloadFile(ctx, srv.URL+"/media.bin", dest, cfg); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	elapsed := time.Since(start)

	events := rec.snapshot()
	if len(events) == 0 {
		t.Fatal("一个事件都没收到，节流断言无从谈起")
	}
	// 上界随实际历时伸缩（慢 runner 上不误判），与 pacer_test.go 同一算法：节流下
	// 每秒最多 1/minFrameInterval 帧，加收尾帧与调度余量。
	maxEvents := int(elapsed/minFrameInterval) + 8
	if len(events) > maxEvents {
		t.Errorf("观察者事件数 %d 超过节流上限 %d（历时 %v）：回调没有走在既有节流之后，"+
			"高频下载会把 SSE 打爆", len(events), maxEvents, elapsed)
	}
}

// TestProgressObserverReportsUnknownTotal 覆盖契约里的 Total=0（长度未知）：
// 服务器用 chunked 传输、不声明 Content-Length 时观察者仍要收到事件，此时 Total=0。
// 终端与 --progress-json 的既有门槛不变（本用例只开观察者）。
//
// 变异验证：把 withProgress 的长度门槛改回只看 resp.ContentLength>0（不给观察者例外），
// 事件数为 0，本用例变红。
func TestProgressObserverReportsUnknownTotal(t *testing.T) {
	const size = 1 << 20
	body := bytes.Repeat([]byte("m"), size)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先 flush 一次强制 chunked：不声明 Content-Length，客户端读到 -1。
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := DownloadFile(ctx, srv.URL+"/media.bin", dest, DownloadConfig{Client: newTestClient(), RetryCount: 1}); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	events := rec.snapshot()
	if len(events) == 0 {
		t.Fatal("长度未知时观察者也该收到事件（Total=0 表示未知）")
	}
	for i, ev := range events {
		if ev.Total != 0 {
			t.Errorf("第 %d 个事件 Total=%d，期望 0（服务器没声明长度）", i, ev.Total)
		}
	}
	if last := events[len(events)-1]; last.Current != size {
		t.Errorf("末事件 Current=%d，期望 %d", last.Current, size)
	}
	if got := fileSizeOrZero(dest); got != size {
		t.Errorf("产物长度 = %d，期望 %d", got, size)
	}
}

// TestProgressObserverCoexistsWithProgressJSON 钉住两种消费者可以同时在场：
// --progress-json 的逐行事件与观察者各自完整（serve 与 CLI 可能同开）。
//
// 变异验证：删掉 renderJSONLoop 里的 notifyObserver 调用，观察者事件为 0，本用例变红。
func TestProgressObserverCoexistsWithProgressJSON(t *testing.T) {
	withProgressJSON(t)
	const size = 2 << 20
	srv := serveMedia(t, size)
	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)

	dest := filepath.Join(t.TempDir(), "out.bin")
	var err error
	_, stderr := captureProgressStreams(t, func() {
		err = DownloadFile(ctx, srv.URL+"/media.bin", dest, DownloadConfig{Client: newTestClient(), RetryCount: 1})
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if events := parseProgressEvents(t, stderr); len(events) == 0 {
		t.Error("--progress-json 的事件缺失")
	}
	events := rec.snapshot()
	if len(events) == 0 {
		t.Fatal("观察者事件缺失（JSON 路径没有回调）")
	}
	last := events[len(events)-1]
	if last.Current != size || last.Total != size {
		t.Errorf("末事件 = %+v，期望 Current==Total==%d", last, size)
	}
}
