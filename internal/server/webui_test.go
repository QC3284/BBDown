package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// 任务 N 的回归网：极简 Web UI（GET /）+ SSE 进度流（GET /events）。
//
// 判据全部是**行为**：HTTP 状态与头部、SSE 帧文本、订阅者计数、事件字段。
// 等待异步副作用一律「轮询 + 上限」（waitFor），不用固定 sleep。

const (
	// eventWaitTimeout 是「等一帧/等一个状态」的上限：慢 runner 上放宽的是上限，不是固定等待。
	eventWaitTimeout = 5 * time.Second
	// eventCleanupTimeout 是清理服务器时等待处理器退出的上限（见 startEventServer）。
	eventCleanupTimeout = 3 * time.Second
)

// externalRefRe 匹配页面里的外链资源：离线可用的判据（不引 CDN / 不引前端框架）。
var externalRefRe = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["'](?:https?:)?//`)

// startEventServer 起一个真实 HTTP 服务器：/events 在连接结束前不会返回，
// httptest.ResponseRecorder 会把处理器粘在 ServeHTTP 上，所以流式用例必须走真实连接。
//
// 清理时带超时地 Close——客户端断开后处理器若仍然挂着（协程泄漏），Close 会一直等下去，
// 超时即判红，而不是把整轮用例拖成挂起。
func startEventServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			srv.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(eventCleanupTimeout):
			t.Errorf("httptest.Server.Close 超时：客户端断开后 /events 处理器仍在运行（订阅/协程泄漏）")
		}
	})
	return srv
}

// openEventStream 打开一个 /events 连接。返回的 closeStream 用来模拟客户端断开；
// 它同时登记为 t.Cleanup（断言失败时也先断开，避免后续清理被挂起的处理器卡住）。
func openEventStream(t *testing.T, timeout time.Duration, url string) (*http.Response, *bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("构造 /events 请求失败: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("连接 /events 失败: %v", err)
	}
	closeStream := func() { cancel(); resp.Body.Close() }
	t.Cleanup(closeStream)
	return resp, bufio.NewReader(resp.Body), closeStream
}

// eventStatus 发一次 /events 请求、取状态码与响应头后立刻断开。
// 走真实连接而不是 ResponseRecorder：门禁或连接上限一旦失效，处理器会转成流式，
// recorder 会把用例挂死；这里有 ctx 上限，失效时得到的是断言红。
func eventStatus(t *testing.T, url string) (int, http.Header) {
	t.Helper()
	resp, _, closeStream := openEventStream(t, eventWaitTimeout, url)
	code, header := resp.StatusCode, resp.Header.Clone()
	closeStream()
	return code, header
}

// readSSEFrame 读一帧原始 SSE 文本（data 行 + 结尾空行）；心跳（":" 注释行）与空行跳过。
// 读不到时由请求 ctx 的 deadline 终止——用例不用固定 sleep。
func readSSEFrame(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case line == "\n" || line == "\r\n":
			if b.Len() == 0 {
				continue
			}
			return b.String() + "\n", nil
		default:
			b.WriteString(line)
		}
	}
}

// decodeFrame 校验一帧是合法 SSE data 帧（data: 前缀 + 双换行）并解析出事件。
func decodeFrame(t *testing.T, frame string) ServerEvent {
	t.Helper()
	if !strings.HasPrefix(frame, "data: ") {
		t.Fatalf("帧缺少 %q 前缀: %q", "data: ", frame)
	}
	if !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("帧没有以空行结尾（SSE 帧 = data 行 + 空行）: %q", frame)
	}
	var ev ServerEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(frame[len("data: "):])), &ev); err != nil {
		t.Fatalf("帧不是合法 JSON: %q: %v", frame, err)
	}
	return ev
}

// waitFor 轮询条件直到成立或超时：等异步副作用不用固定 sleep，判据仍由调用方断言。
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

func newTestTask(jobID string) *DownloadTask {
	task := &DownloadTask{
		JobID:  jobID,
		Aid:    "BV1xx411c7mD",
		URL:    "https://www.bilibili.com/video/BV1xx411c7mD",
		Status: StatusQueued,
	}
	task.mu = &sync.Mutex{}
	return task
}

// GET / 返回 200 + 正确 Content-Type + 一屏内所需的关键元素；页面必须离线可用（无外链）。
//
// 变异验证：删掉内嵌页面（rm internal/server/webui/index.html）→ 处理器回 500，本用例变红
// （go:embed all:webui 仍能编译，所以红的是断言而不是构建）。
func TestWebUIIndexServed(t *testing.T) {
	h := newLoopbackServer().buildHandler()
	rec := do(h, http.MethodGet, "http://127.0.0.1:23333/", "127.0.0.1:23333", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!DOCTYPE html>",
		`id="progress"`,                                        // 进度条
		`id="percent"`, `id="speed"`, `id="eta"`, `id="total"`, // 百分比/速率/ETA/总量
		`id="events"`, // 最近事件列表
		`id="empty"`,  // 空态：无任务时页面正常显示
		"EventSource", `"/events"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺少关键元素 %q", want)
		}
	}
	if m := externalRefRe.FindString(body); m != "" {
		t.Errorf("页面引用了外部资源（离线不可用）：%q", m)
	}
	// 未注册路径仍是 404（与新增页面之前逐字一致），非 GET 是 405。
	if rec := do(h, http.MethodGet, "http://127.0.0.1:23333/nope", "127.0.0.1:23333", "", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404（页面不该接管未注册路径）", rec.Code)
	}
	if rec := do(h, http.MethodPost, "http://127.0.0.1:23333/", "127.0.0.1:23333", "http://127.0.0.1:23333", "application/json", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", rec.Code)
	}
}

// 首帧必须是合法 SSE（data: 前缀 + 双换行），Content-Type 必须是 text/event-stream。
//
// 变异验证：去掉写首帧后的 Flush（服务端把响应头/帧缓存在缓冲区里）→ 客户端永远等不到首帧，
// 本用例变红（读请求被自己的 ctx deadline 终止，红的是断言而不是构建）。
func TestWebUIEventsFirstFrameIsSSE(t *testing.T) {
	s := newLoopbackServer()
	srv := startEventServer(t, s.buildHandler())

	resp, br, _ := openEventStream(t, eventWaitTimeout, srv.URL+"/events")
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	frame, err := readSSEFrame(br)
	if err != nil {
		t.Fatalf("读首帧失败（服务端没有 flush？）: %v", err)
	}
	if ev := decodeFrame(t, frame); ev.Type != EventHello {
		t.Errorf("首帧 type = %q, want %q", ev.Type, EventHello)
	}
}

// 内部广播（发布者就是任务生命周期用的那条路径）必须到达已连接的客户端。
func TestWebUIEventsDeliversBroadcast(t *testing.T) {
	s := newLoopbackServer()
	srv := startEventServer(t, s.buildHandler())

	resp, br, _ := openEventStream(t, eventWaitTimeout, srv.URL+"/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events = %d, want 200", resp.StatusCode)
	}
	// hello 是「订阅已注册」的确定性同步点：读到它之后发布的广播一定有人接。
	if _, err := readSSEFrame(br); err != nil {
		t.Fatalf("握手帧读取失败: %v", err)
	}

	s.events.publish(ServerEvent{
		Type: EventTaskProgress, JobID: "job-1", Status: StatusRunning,
		Percent: 42.5, Downloaded: 2048, Total: 4096, Speed: 512,
	})

	var got ServerEvent
	for i := 0; i < 4; i++ { // 上限：最多读 4 帧
		frame, err := readSSEFrame(br)
		if err != nil {
			t.Fatalf("第 %d 帧读取失败: %v", i+1, err)
		}
		ev := decodeFrame(t, frame)
		if ev.JobID == "job-1" {
			got = ev
			break
		}
	}
	if got.JobID != "job-1" {
		t.Fatalf("客户端没有收到内部广播的事件")
	}
	if got.Type != EventTaskProgress || got.Status != StatusRunning || got.State != StateProgress ||
		got.Percent != 42.5 || got.Downloaded != 2048 || got.Total != 4096 || got.Speed != 512 || got.Seq == 0 {
		t.Errorf("事件字段不符: %+v", got)
	}
}

// publishTaskEvent 的字段映射：percent 用 --progress-json 的 0~100 口径（API 的 Progress 仍是 0~1）。
func TestPublishTaskEventFieldMapping(t *testing.T) {
	s := newLoopbackServer()
	client, ok := s.events.subscribe()
	if !ok {
		t.Fatal("订阅失败")
	}
	defer s.events.unsubscribe(client)

	task := newTestTask("job-pub")
	task.mu.Lock()
	task.Title = "标题"
	task.Aid = "BV1xx411c7mD"
	task.Status = StatusRunning
	task.Progress = 0.25
	task.TotalDownloadedBytes = 4096
	task.mu.Unlock()

	s.publishTaskEvent(EventTaskProgress, task, StateProgress)

	select {
	case ev := <-client.ch:
		if ev.Type != EventTaskProgress || ev.JobID != "job-pub" || ev.Aid != "BV1xx411c7mD" ||
			ev.Title != "标题" || ev.Status != StatusRunning || ev.State != StateProgress {
			t.Errorf("事件字段不符: %+v", ev)
		}
		if ev.Percent != 25 {
			t.Errorf("percent = %v, want 25（0~100 口径）", ev.Percent)
		}
		if ev.Downloaded != 4096 {
			t.Errorf("downloaded = %d, want 4096", ev.Downloaded)
		}
		if ev.Seq == 0 || ev.Time == 0 {
			t.Errorf("seq/time 未填充: %+v", ev)
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("没有收到事件")
	}
}

// 产物字节只计一次：重试/断点续传会把同一路径重复上报，重复计数会虚增 downloaded。
func TestOnArtifactSavedCountsBytesOnce(t *testing.T) {
	s := newLoopbackServer()
	task := newTestTask("job-bytes")
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	s.onArtifactSaved(task, path)
	s.onArtifactSaved(task, path) // 重试/续传的重复上报
	s.onArtifactSaved(task, "")   // 空路径不计数

	snap := task.Snapshot()
	if snap.TotalDownloadedBytes != 1024 {
		t.Errorf("TotalDownloadedBytes = %d, want 1024（重复上报不能重复计数）", snap.TotalDownloadedBytes)
	}
	if len(snap.SavePaths) != 1 {
		t.Errorf("SavePaths = %v, want 1 条", snap.SavePaths)
	}

	client, ok := s.events.subscribe() // 订阅时回放历史 = 观察这条路径真正发了什么
	if !ok {
		t.Fatal("订阅失败")
	}
	defer s.events.unsubscribe(client)
	if n := len(client.ch); n != 1 {
		t.Errorf("事件数 = %d, want 1（重复路径不该重复发事件）", n)
	}
}

// 客户端断开必须注销订阅并结束处理器协程：轮询订阅者计数（有上限），不用固定 sleep。
//
// 变异验证：去掉 defer unsubscribe（订阅者计数不归零）或去掉 select 里的 ctx.Done() 分支
// （空闲连接上的处理器一直挂着）→ 本用例变红。
func TestWebUIEventsClientDisconnectReleasesSubscriber(t *testing.T) {
	s := newLoopbackServer()
	srv := startEventServer(t, s.buildHandler())

	for cycle := 1; cycle <= 3; cycle++ {
		resp, br, closeStream := openEventStream(t, eventWaitTimeout, srv.URL+"/events")
		if resp.StatusCode != http.StatusOK {
			closeStream()
			t.Fatalf("第 %d 次连接 = %d, want 200", cycle, resp.StatusCode)
		}
		if _, err := readSSEFrame(br); err != nil { // 读到 hello = 订阅已注册
			closeStream()
			t.Fatalf("第 %d 次握手失败: %v", cycle, err)
		}
		if n := s.events.clientCount(); n != 1 {
			closeStream()
			t.Fatalf("第 %d 次握手后订阅者 = %d, want 1", cycle, n)
		}

		closeStream() // 客户端断开
		if !waitFor(eventWaitTimeout, func() bool { return s.events.clientCount() == 0 }) {
			t.Fatalf("第 %d 次断开后仍有 %d 个订阅者：/events 泄漏了订阅与协程", cycle, s.events.clientCount())
		}
	}
}

// 慢客户端（队列满）只能丢帧，不能阻塞发布者——发布发生在下载协程里。
func TestEventHubPublishNeverBlocksOnSlowClient(t *testing.T) {
	h := newEventHub(4)
	c, ok := h.subscribe()
	if !ok {
		t.Fatal("订阅失败")
	}
	defer h.unsubscribe(c)

	done := make(chan struct{})
	go func() {
		for i := 0; i < eventClientBuffer*3; i++ {
			h.publish(ServerEvent{Type: EventTaskProgress})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(eventWaitTimeout):
		t.Fatal("发布被慢客户端阻塞了")
	}
	if n := len(c.ch); n != eventClientBuffer {
		t.Errorf("慢客户端的队列 = %d, want 恰好 %d（多出来的帧应当被丢掉）", n, eventClientBuffer)
	}
}

// 晚连接/自动重连的客户端要能立刻看到最近事件（回放），且回放必须有界。
func TestEventHubReplaysBoundedHistory(t *testing.T) {
	h := newEventHub(4)
	h.publish(ServerEvent{Type: EventTaskStart, JobID: "j1"})
	h.publish(ServerEvent{Type: EventTaskDone, JobID: "j1", Status: StatusSucceeded})

	c, ok := h.subscribe()
	if !ok {
		t.Fatal("订阅失败")
	}
	defer h.unsubscribe(c)
	if n := len(c.ch); n != 2 {
		t.Errorf("回放条数 = %d, want 2", n)
	}

	for i := 0; i < eventHistory*2; i++ {
		h.publish(ServerEvent{Type: EventTaskProgress})
	}
	c2, ok := h.subscribe()
	if !ok {
		t.Fatal("订阅失败")
	}
	defer h.unsubscribe(c2)
	if n := len(c2.ch); n != eventHistory {
		t.Errorf("回放条数 = %d, want %d（历史必须有界）", n, eventHistory)
	}
}

// 门禁：页面不含用户数据（与 /health 同级，不需要 token）；事件流带任务数据，配了
// --serve-token 就必须带 token（EventSource 不能自定义请求头，故同时支持 ?token=）。
// 且 /events 的失败计数与 API 的 authGuard **分开**：浏览器自动重连不能把用户锁在 API 外。
func TestWebUIEventsTokenGate(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:23333", 1, "s3cret", "")
	h := s.buildHandler()

	if rec := do(h, http.MethodGet, "http://127.0.0.1:23333/", "127.0.0.1:23333", "", "", ""); rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200（页面本身不含用户数据）", rec.Code)
	}
	srv := startEventServer(t, h)
	// 无 token → 401。
	if code, _ := eventStatus(t, srv.URL+"/events"); code != http.StatusUnauthorized {
		t.Errorf("无 token 的 /events = %d, want 401", code)
	}
	// 连续失败：先是 401，达到锁定期后 429 + Retry-After（与 API 同一套规则）。
	// 上面那次探测已经是第 1 次失败，所以这里再补 maxAuthFailures-1 次。
	for i := 1; i < maxAuthFailures; i++ {
		if code, _ := eventStatus(t, srv.URL+"/events"); code != http.StatusUnauthorized {
			t.Fatalf("累计第 %d 次无 token 请求 = %d, want 401", i+1, code)
		}
	}
	if code, header := eventStatus(t, srv.URL+"/events"); code != http.StatusTooManyRequests {
		t.Errorf("达到失败上限后 = %d, want 429", code)
	} else if header.Get("Retry-After") == "" {
		t.Error("429 必须带 Retry-After")
	}

	// 两种带法都要能连上并读到首帧：?token= 是页面透传的路径，X-Serve-Token 是 CLI/脚本的路径。
	resp, br, _ := openEventStream(t, eventWaitTimeout, srv.URL+"/events?token=s3cret")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("?token= 的 /events = %d, want 200", resp.StatusCode)
	} else if _, err := readSSEFrame(br); err != nil {
		t.Errorf("?token= 的 /events 读首帧失败: %v", err)
	}
	// 请求头（CLI/脚本的路径）。
	ctx, cancel := context.WithTimeout(context.Background(), eventWaitTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("X-Serve-Token", "s3cret")
	headerResp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("X-Serve-Token 连接失败: %v", err)
	}
	if headerResp.StatusCode != http.StatusOK {
		t.Errorf("X-Serve-Token 的 /events = %d, want 200", headerResp.StatusCode)
	} else if _, err := readSSEFrame(bufio.NewReader(headerResp.Body)); err != nil {
		t.Errorf("X-Serve-Token 的 /events 读首帧失败: %v", err)
	}
	cancel()
	headerResp.Body.Close()

	// 但 API 本身不受影响：/events 的失败不该把用户锁在 API 外面。
	apiReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:23333/get-tasks", nil)
	apiReq.Host = "127.0.0.1:23333"
	apiReq.Header.Set("X-Serve-Token", "s3cret")
	apiRec := httptest.NewRecorder()
	h.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Errorf("GET /get-tasks = %d, want 200：/events 的失败计数不应锁住 API", apiRec.Code)
	}
}

// 连接上限：每个 SSE 连接占一个 goroutine + 一条队列，必须有上限（满了 503 + Retry-After）。
func TestWebUIEventsClientCap(t *testing.T) {
	s := newLoopbackServer()
	held := make([]*eventClient, 0, maxEventClients)
	for i := 0; i < maxEventClients; i++ {
		c, ok := s.events.subscribe()
		if !ok {
			t.Fatalf("第 %d 个订阅应当成功（上限 %d）", i+1, maxEventClients)
		}
		held = append(held, c)
	}
	defer func() {
		for _, c := range held {
			s.events.unsubscribe(c)
		}
	}()

	srv := startEventServer(t, s.buildHandler())
	// 满了 → 503 + Retry-After（真实连接：上限一旦失效，处理器转成流式，
	// 有上限的 ctx 会把它变成断言红，而不是把用例挂死）。
	if code, header := eventStatus(t, srv.URL+"/events"); code != http.StatusServiceUnavailable {
		t.Fatalf("连接数达到上限时 = %d, want 503", code)
	} else if header.Get("Retry-After") == "" {
		t.Error("503 必须带 Retry-After，客户端才知道何时再来")
	}

	// 空出一个位置后又能连上。
	s.events.unsubscribe(held[0])
	resp, br, _ := openEventStream(t, eventWaitTimeout, srv.URL+"/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("空出位置后 = %d, want 200", resp.StatusCode)
	}
	if _, err := readSSEFrame(br); err != nil {
		t.Fatalf("空出位置后读首帧失败: %v", err)
	}
}
