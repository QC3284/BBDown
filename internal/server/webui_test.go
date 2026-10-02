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
	"sort"
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
	h := newLoopbackServer(t).buildHandler()
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
	s := newLoopbackServer(t)
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
	s := newLoopbackServer(t)
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
	s := newLoopbackServer(t)
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
	s := newLoopbackServer(t)
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
	s := newLoopbackServer(t)
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
	s := newLoopbackServer(t)
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

// ============================================================================
// t8：Web UI 重设计的契约（任务列表 + 详情卡 + 新增任务表单）
// ============================================================================
//
// 这一组用例是**静态契约**：页面本身就是交付物（内嵌在二进制、由 GET / 原样送出），
// 所以断言直接打在**同一份嵌入字节**上（webUIAssets.ReadFile，与 handleIndex 同源，
// 不驱动副本）。Go 侧的测试链路里没有 JS 引擎（go.mod 只有 cobra/qrcode/term，不为主
// 模块引入 JS 运行时），「点一下表单」「浏览器触发 onerror」这类只能靠浏览器执行的行为，
// 用**结构性契约**钉住：禁止 HTML 注入点、文本写入收敛到唯一出口、请求体字段名集合、
// 预设与断线兜底的接线必须存在。每条用例都写明变异验证（改回什么会红）。

// dquote 是双引号字符：页面里的属性/字符串形态都带引号，而 Go 字面量里写引号要转义，
// 用例里用拼接表达反而更清楚（也避免把转义写错）。
var dquote = string(rune(34))

// embeddedWebUIPage 返回内嵌页面的字节（与 handleIndex 送出的是同一份）。
func embeddedWebUIPage(t *testing.T) string {
	t.Helper()
	page, err := webUIAssets.ReadFile(webUIPagePath)
	if err != nil || len(page) == 0 {
		t.Fatalf("内嵌页面缺失（%s）：%v", webUIPagePath, err)
	}
	return string(page)
}

// htmlSinks 是「能把字符串当 HTML/JS 执行」的调用点。按**调用点形态**匹配（点号限定或带
// 左括号），这样注释里解释「本页不使用 innerHTML」不会自己踩雷；但注释里若写出 .innerHTML
// 这种调用形态仍会判红——宁可误报，不可漏报。
var htmlSinks = []string{
	".innerHTML",
	".outerHTML",
	"insertAdjacentHTML(",
	"document.write(",
	"eval(",
	"new Function(",
	"srcdoc=",
	"createContextualFragment(",
	".setAttribute(\"on",
}

// TestWebUIXSSSinksAndTextOnly 钉住 XSS 纪律：任务标题 / URL / aid / 错误文本都是用户可控
// 数据（由 B 站页面与本地错误拼出），页面必须**只**经文本节点落字。
//
// 判据：
//  1. 全页面没有任何 HTML/JS 注入调用点；
//  2. 文本写入收敛到**唯一**出口 setText()（全页 textContent 只出现一次，就在它里面）；
//  3. 渲染路径确实都用它（setText 调用点数量下限 + 标题这两个最直接的注入面必须走它）。
//
// 威胁样例（交付说明里的两条）：标题 <img src=x onerror=alert(1)> 与
// </div><script>alert(1)</script>——经 textContent 落字时它们是纯文本，不产生元素、
// 不执行脚本；一旦改回 innerHTML，同一份字节就会把标题当 HTML 解析。
//
// 变异验证：把标题渲染改回 innerHTML（name.innerHTML = label(t)）→ 第 1 条红；
// 让 setText 不再用 textContent → 第 2 条红。
func TestWebUIXSSSinksAndTextOnly(t *testing.T) {
	page := embeddedWebUIPage(t)

	if !strings.Contains(page, "<script>") {
		t.Fatal("页面里没有内联脚本：XSS 判据无从谈起")
	}
	for _, sink := range htmlSinks {
		if strings.Contains(page, sink) {
			t.Errorf("页面出现 HTML/JS 注入调用点 %q：用户可控的标题/URL/错误文本会被当标记解析", sink)
		}
	}

	if n := strings.Count(page, ".textContent"); n != 1 {
		t.Errorf("文本写入点 = %d 处（.textContent），want 恰好 1（收敛到 setText）：口子越少越好审", n)
	}
	if !strings.Contains(page, "function setText(el, value)") {
		t.Error("缺少唯一文本写入点 setText(el, value)")
	}
	if n := strings.Count(page, "setText("); n < 10 {
		t.Errorf("setText 调用点 = %d，want >= 10：列表/详情/事件流/表单提示都该经它落字", n)
	}
	// 两个最直接的注入面（列表项标题、详情标题）必须走 setText —— 变异验证的靶点。
	for _, want := range []string{"setText(name, label(t))", "setText(title, label(t))"} {
		if !strings.Contains(page, want) {
			t.Errorf("用户可控的标题渲染没有走 setText：缺少 %q", want)
		}
	}
}

// TestWebUIFormFieldsMatchAddTaskWhitelist 钉住表单 → /add-task 的字段名契约。
//
// 页面构建请求体时**直接遍历 [data-field] 元素**（没有第二份手写映射可以漂移），所以
// 「页面声明的 data-field 集合」==「请求体的键集合」。这里把它与同包的 addTaskAllowedFields
// （t7/t13 的白名单，服务端严格模式只认这 15 个）双向比对：少一个 = 表单功能缺失，
// 多一个 = 一提交就被 400 点名。
//
// 变异验证：把任一 data-field 改名/删掉（如 select_page → p）→ 双向差集非空，本用例红。
func TestWebUIFormFieldsMatchAddTaskWhitelist(t *testing.T) {
	page := embeddedWebUIPage(t)

	marker := "data-field=" + dquote
	got := map[string]bool{}
	rest := page
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			break
		}
		rest = rest[i+len(marker):]
		j := strings.IndexByte(rest, dquote[0])
		if j < 0 {
			break
		}
		got[rest[:j]] = true
		rest = rest[j+1:]
	}
	want := map[string]bool{}
	for _, f := range addTaskAllowedFields {
		want[f] = true
	}

	var missing, extra []string
	for f := range want {
		if !got[f] {
			missing = append(missing, f)
		}
	}
	for f := range got {
		if !want[f] {
			extra = append(extra, f)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("表单缺少白名单字段 %v（共 %d 个 data-field，白名单 %d 个）", missing, len(got), len(want))
	}
	if len(extra) > 0 {
		t.Errorf("表单出现白名单外的字段 %v：/add-task 是严格模式，会 400 点名", extra)
	}

	// 请求体确实来自遍历（而不是手写映射），且「未设置」的字段被跳过
	// （后端用指针字段区分「未传」与 false；select_page 留空表示全部分P）。
	if !strings.Contains(page, "querySelectorAll("+dquote+"[data-field]"+dquote+")") {
		t.Error("请求体不是从 [data-field] 遍历构造的：字段名可能与表单声明漂移")
	}
	if !strings.Contains(page, "if (value === null) { continue; }") {
		t.Error("构建请求体时没有跳过「未设置」的字段：会把空值/默认值也发出去")
	}
}

// TestWebUIReconnectAndFallbackWiring 钉住断线语义：
// SSE 断开 → 连接状态显示「已断开」→ EventSource **自动重连**（页面不得手动 close 或重建）
// → 同时用 /get-tasks 兜底刷新（断线期间新加的任务与终态不会丢）。
//
// 变异验证：删掉 onerror 里的 refreshTasks()（断线后不再兜底）→ 红；
// 在 onerror 里加 es.close()（浏览器不再自动重连）→ 红。
func TestWebUIReconnectAndFallbackWiring(t *testing.T) {
	page := embeddedWebUIPage(t)

	start := strings.Index(page, "es.onerror = function")
	if start < 0 {
		t.Fatal("页面没有 es.onerror：断线状态与兜底刷新都无从触发")
	}
	end := strings.Index(page[start:], "};")
	if end < 0 {
		t.Fatal("es.onerror 不是一个完整赋值语句")
	}
	onError := page[start : start+end]
	if !strings.Contains(onError, "已断开") {
		t.Error("onerror 没有把连接状态置为「已断开」")
	}
	if !strings.Contains(onError, "refreshTasks()") {
		t.Error("onerror 没有触发 /get-tasks 兜底刷新：断线期间的新任务会整段丢失")
	}
	if strings.Contains(onError, "close(") || strings.Contains(onError, "EventSource(") {
		t.Error("onerror 里关闭/重建了事件流：EventSource 的自动重连被掐掉了")
	}
	if strings.Contains(page, "es.close(") {
		t.Error("页面手动 close 了事件流：自动重连不再成立")
	}

	if !strings.Contains(page, "new EventSource("+dquote+"/events"+dquote) {
		t.Error("页面没有订阅 /events")
	}
	if !strings.Contains(page, "fetch("+dquote+"/get-tasks"+dquote) {
		t.Error("页面没有 /get-tasks 兜底数据源")
	}
	if !strings.Contains(page, "setInterval(refreshTasks") {
		t.Error("缺少周期性的 /get-tasks 兜底刷新（晚连接/断线期间也要能补齐任务列表）")
	}
}

// TestWebUIPresetPersistenceWiring 钉住表单预设的 localStorage 接线：
// 加载时恢复、提交/保存时写入、重置按钮清掉并回到默认值。
//
// 变异验证：删掉初始化处的 loadPreset() 调用（只剩定义，打开页面不再恢复预设）→ 红。
func TestWebUIPresetPersistenceWiring(t *testing.T) {
	page := embeddedWebUIPage(t)

	// 预设键必须是**同一个常量**（定义 + set/get/remove 都引用它）；散落字面量会改一处漏一处。
	keyDecl := "var PRESET_KEY = " + dquote + "bbdown.serve.addTask.preset" + dquote
	if !strings.Contains(page, keyDecl) {
		t.Errorf("缺少预设键常量声明：%s", keyDecl)
	}
	for _, call := range []string{"localStorage.setItem(", "localStorage.getItem(", "localStorage.removeItem("} {
		if !strings.Contains(page, call) {
			t.Errorf("缺少预设接线 %s", call)
		}
	}
	if n := strings.Count(page, "PRESET_KEY"); n < 4 {
		t.Errorf("PRESET_KEY 出现 %d 次（定义 + set/get/remove 各一），want >= 4", n)
	}
	// 初始化必须真的调一次 loadPreset()：只有定义没有调用 = 打开页面不会恢复预设。
	if n := strings.Count(page, "loadPreset()"); n < 2 {
		t.Errorf("loadPreset() 出现 %d 次：除了定义还必须有一处初始化调用，否则预设不会恢复", n)
	}
	for _, want := range []string{"getElementById(" + dquote + "btn-reset-preset" + dquote + ")", "getElementById(" + dquote + "btn-save-preset" + dquote + ")"} {
		if !strings.Contains(page, want) {
			t.Errorf("缺少预设按钮接线：%s", want)
		}
	}
}

// TestWebUIActionRequestsSatisfyGuardMiddleware 钉住取消/移除的请求形态：
// guardMiddleware（server.go）对 /cancel 与 /remove-finished 这类写端点要求
// Content-Type: application/json（防跨站「简单请求」的 CSRF 闸门），不带就是 415。
// 这条是 **t8 真机冒烟抓到的**：页面最初只带 token 头，DELETE 直接被 415 挡掉。
//
// 变异验证：把 writeOptions 里的 Content-Type 去掉（或绕开它自己拼 fetch）→ 本用例红。
func TestWebUIActionRequestsSatisfyGuardMiddleware(t *testing.T) {
	page := embeddedWebUIPage(t)

	start := strings.Index(page, "function writeOptions(")
	if start < 0 {
		t.Fatal("缺少取消/移除共用的 writeOptions：写请求的形态没有单一出口")
	}
	body := page[start:]
	if j := strings.Index(body, "\n  }"); j >= 0 {
		body = body[:j]
	}
	for _, want := range []string{"Content-Type", "application/json"} {
		if !strings.Contains(body, want) {
			t.Errorf("写请求没有带 %s：guardMiddleware 会回 415（CSRF 闸门）", want)
		}
	}
	for _, want := range []string{"writeOptions(" + dquote + "POST" + dquote + ")", "writeOptions(" + dquote + "DELETE" + dquote + ")"} {
		if !strings.Contains(page, want) {
			t.Errorf("取消/移除没有走 writeOptions：缺少 %s", want)
		}
	}
}

// TestWebUIExternalRefsAndSizeBudget 钉住「单文件、离线可用、体积有预算」：
// 不引 CDN/框架/远程字体/图片（属性里不得出现协议相对或绝对 URL），页面字节数 ≤ 64KiB，
// 且 GET / 送出的就是这份嵌入字节（用例驱动的是交付物本身，不是副本）。
//
// 变异验证：加一行 <script src=//cdn...> → 外链断言红；把体积撑过 64KiB → 预算断言红。
func TestWebUIExternalRefsAndSizeBudget(t *testing.T) {
	page := embeddedWebUIPage(t)

	if m := externalRefRe.FindString(page); m != "" {
		t.Errorf("页面引用了外部资源（离线不可用）：%q", m)
	}
	for _, bad := range []string{"@import", "<script src", "<iframe", "googleapis", "cdn."} {
		if strings.Contains(page, bad) {
			t.Errorf("页面出现外部依赖标记 %q：单文件页面不允许", bad)
		}
	}
	const budget = 64 << 10
	if len(page) > budget {
		t.Errorf("页面体积 = %d 字节，超过 %d 字节预算", len(page), budget)
	}

	rec := do(newLoopbackServer(t).buildHandler(), http.MethodGet, "http://127.0.0.1:23333/",
		"127.0.0.1:23333", "", "", "")
	if rec.Body.String() != page {
		t.Error("GET / 送出的字节与内嵌页面不一致：用例断言的不是真正的交付物")
	}
}

// TestWebUIRedesignLayoutContract 钉住重设计的结构：左侧任务列表 + 右侧详情卡 + 新增任务表单，
// 四个状态色点、迷你/大进度条、统计网格、速率曲线、事件流、操作按钮（暂停置灰并标注
// 「API 暂不支持」）、深色优先与窄屏单列。
//
// 变异验证：删掉任一 id/样式钩子（如 id=task-list、.dot.bad、media 查询）→ 本用例红。
func TestWebUIRedesignLayoutContract(t *testing.T) {
	page := embeddedWebUIPage(t)
	q := func(name string) string { return dquote + name + dquote }

	for _, want := range []string{
		// ① 左栏任务列表（点选切换右侧详情）
		"id=" + q("task-list"), "id=" + q("empty"), "id=" + q("conn"),
		// 状态色点：排队灰 / 运行蓝 / 完成绿 / 失败红
		".dot.queued", ".dot.running", ".dot.ok", ".dot.bad",
		// ② 详情卡：大进度条 + 统计网格 + 速率曲线 + 事件流
		"id=" + q("detail"), "id=" + q("task-title"), "id=" + q("detail-status"), "id=" + q("progress"),
		"id=" + q("percent"), "id=" + q("downloaded"), "id=" + q("total"), "id=" + q("speed"), "id=" + q("eta"),
		"id=" + q("spark"), "id=" + q("spark-line"), "id=" + q("events"),
		// 操作按钮：取消 / 重试 / 移除 / 暂停（置灰）
		"id=" + q("btn-cancel"), "id=" + q("btn-retry"), "id=" + q("btn-remove"), "id=" + q("btn-pause"),
		"API 暂不支持",
		// ③ 新增任务表单
		"id=" + q("add-form"), "id=" + q("add-submit"), "id=" + q("form-msg"),
		// ④ 响应式 + 深色优先
		"prefers-color-scheme", "@media (max-width:900px)",
		// ⑤ 既有 API：取消 / 移除 / 新增
		q("/cancel/"), q("/remove-finished/"), q("/add-task"),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("页面缺少重设计要素 %q", want)
		}
	}

	// 「暂停」必须是**置灰**的（API 暂不支持），不能是个能点的假按钮。
	i := strings.Index(page, "id="+q("btn-pause"))
	if i < 0 {
		t.Fatal("找不到暂停按钮")
	}
	tag := page[i:]
	if j := strings.IndexByte(tag, '>'); j >= 0 {
		tag = tag[:j]
	}
	if !strings.Contains(tag, "disabled") {
		t.Error("暂停按钮没有 disabled：API 暂不支持的操作不能看起来可用")
	}
}
