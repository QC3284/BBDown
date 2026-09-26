package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// 任务 N：SSE 进度流（GET /events）——serve 的 Web UI 数据源。
//
// 事件字段不另发明语义，全部沿用仓库已有的两类 JSON 输出（见 internal/download）：
//   - percent / downloaded / total / speed / state 与 --progress-json 的进度事件逐字段同义：
//     percent 是 0~100；总量未知时 total=0、percent=0；state: progress=进行中，done=本次结束。
//   - job_id / aid / url / title / status 与任务 API（/get-tasks）和 --info-json 的元数据同源。
//
// 事件类型：task_start（任务被接受，Status=Queued）→ task_progress（开始执行 / 拿到元数据 /
// 每件产物落盘）→ task_done（成功或取消）或 task_failed（失败）。
const (
	EventTaskStart    = "task_start"
	EventTaskProgress = "task_progress"
	EventTaskDone     = "task_done"
	EventTaskFailed   = "task_failed"

	// EventHello 是连接建立后的首帧：告诉页面「订阅已生效」（浏览器 onopen 之外再给一个
	// 应用层握手），同时给用例一个确定性同步点——读到它就不必 sleep 也能确定订阅已注册。
	EventHello = "hello"

	// StateProgress / StateDone 沿用 --progress-json 的 state 语义。
	StateProgress = "progress"
	StateDone     = "done"
)

const (
	// maxEventClients 限制同时打开的 SSE 连接数：每个连接占一个 goroutine 加一条事件队列，
	// 不设上限的话，一个只读接口就成了内存/协程放大器（与本包 queryLimiter、acceptLimiter 同一考虑）。
	maxEventClients = 64
	// eventClientBuffer 是单连接的事件队列深度。队列满时**丢事件**（进度流本就允许合并/丢帧），
	// 但绝不阻塞发布者：下载协程不能被一个卡住的浏览器拖住。
	eventClientBuffer = 64
	// eventHistory 是新连接的回放深度：页面晚连或 EventSource 自动重连时，
	// 立刻就能看到最近的任务状态，而不是干等下一次状态变化。
	eventHistory = 64
)

// eventHeartbeatInterval 是心跳（SSE 注释帧）间隔。变量而非常量：用例可以收窄。
// 空闲连接上服务端只有靠写才会发现客户端已经消失，心跳同时承担「探活 + 防中间代理空闲超时」。
var eventHeartbeatInterval = 25 * time.Second

// ServerEvent 是一条 SSE 事件，也是本端点的对外契约（JSON 字段名即契约）。
type ServerEvent struct {
	Seq        int64      `json:"seq"`  // 单调递增；客户端据此在重连回放时去重
	Type       string     `json:"type"` // task_start / task_progress / task_done / task_failed / hello
	Time       int64      `json:"time"` // unix 毫秒
	JobID      string     `json:"job_id,omitempty"`
	Aid        string     `json:"aid,omitempty"`
	URL        string     `json:"url,omitempty"`
	Title      string     `json:"title,omitempty"`
	Status     TaskStatus `json:"status,omitempty"` // Queued / Running / Succeeded / Failed / Cancelled
	Percent    float64    `json:"percent"`          // 0~100，总量未知时 0
	Downloaded int64      `json:"downloaded"`       // 已下载字节
	Total      int64      `json:"total"`            // 总字节，未知为 0
	Speed      float64    `json:"speed"`            // 字节/秒
	State      string     `json:"state"`            // progress=进行中，done=本次结束
	Error      string     `json:"error,omitempty"`  // 已脱敏（maskTaskError）
}

// frame 把事件渲染成一帧 SSE 文本：一行 data + 一个空行。
// 一次 Write 写完整帧，消费方不会读到半帧；JSON 编码不会产生裸换行，data 永远只占一行。
func (e ServerEvent) frame() ([]byte, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 0, len(body)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, body...)
	frame = append(frame, '\n', '\n')
	return frame, nil
}

// eventClient 是一个已连接的 SSE 客户端。
type eventClient struct {
	ch chan ServerEvent
}

// eventHub 是进程内的事件总线：发布者只投递一次，所有在线客户端各拿一份，
// 同时保留最近 eventHistory 条供新连接回放。
type eventHub struct {
	mu      sync.Mutex
	clients map[*eventClient]struct{}
	history []ServerEvent
	seq     int64
	limit   int
}

func newEventHub(limit int) *eventHub {
	return &eventHub{clients: map[*eventClient]struct{}{}, limit: limit}
}

// subscribe 注册一个客户端并回放最近事件；达到上限时返回 false。
func (h *eventHub) subscribe() (*eventClient, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.limit {
		return nil, false
	}
	c := &eventClient{ch: make(chan ServerEvent, eventClientBuffer)}
	h.clients[c] = struct{}{}
	for _, ev := range h.history {
		select {
		case c.ch <- ev:
		default: // 回放深度不超过队列深度，正常不会走到
		}
	}
	return c, true
}

// unsubscribe 注销客户端。刻意**不关闭** channel：发布方在锁内做非阻塞发送，
// 关闭只会让消费者收到零值帧；断开的连接由 channel 自身回收。
func (h *eventHub) unsubscribe(c *eventClient) {
	if c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// publish 广播一条事件：分配序号、记入回放历史、投入每个在线客户端的队列。
// 队列满的客户端丢这一帧（不能阻塞发布者——发布发生在下载协程里）。
func (h *eventHub) publish(ev ServerEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev.Seq = h.seq
	if ev.Time == 0 {
		ev.Time = time.Now().UnixMilli()
	}
	if ev.State == "" {
		ev.State = StateProgress
	}
	h.history = append(h.history, ev)
	if len(h.history) > eventHistory {
		h.history = append(h.history[:0], h.history[len(h.history)-eventHistory:]...)
	}
	for c := range h.clients {
		select {
		case c.ch <- ev:
		default:
		}
	}
}

// clientCount 是当前在线客户端数（用例据此断言断开后不留订阅者）。
func (h *eventHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// handleEvents 是 SSE 进度流（GET /events）。
//
// 门禁（与既有路径的关系，逐条说明）：
//   - tokenMiddleware 的 isAPI 判定与语义**未改**（/events 不是 API 路径，不走那个中间件）；
//   - 但事件流带任务标题等用户数据，所以配了 --serve-token 的部署里 /events **必须**带 token：
//     除 X-Serve-Token 头外还接受 ?token= 查询参数（浏览器的 EventSource 不能自定义请求头，
//     页面从自己的 URL 透传这个参数）。未配 token 时等价于开放，与 /health 同级；
//   - 两者都仍受 guardMiddleware 约束（Host 校验 + X-Content-Type-Options: nosniff +
//     Cache-Control: no-store），DNS rebinding 一类的请求在这里就被挡掉了。
//
// 认证失败使用**独立**的 eventsAuth 计数器：EventSource 会自动重连，若与 API 共用 authGuard，
// 一个没带 token 的页面重试十次就会把同一个浏览器锁在 API 外面（跨端点拒绝服务）。
func (s *APIServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !s.eventTokenAllowed(r) {
		client := s.clientKey(r)
		if retry, blocked := s.eventsAuth.blocked(client); blocked {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retry.Seconds())+1))
			http.Error(w, `{"error":"too many failed attempts"}`, http.StatusTooManyRequests)
			return
		}
		s.eventsAuth.fail(client)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if s.serveToken != "" {
		s.eventsAuth.reset(s.clientKey(r))
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}
	client, ok := s.events.subscribe()
	if !ok {
		w.Header().Set("Retry-After", "5")
		http.Error(w, `{"error":"too many event clients"}`, http.StatusServiceUnavailable)
		return
	}

	// 注销必须挂在 defer 上：客户端一断，订阅就从总线里消失，不留 goroutine / 队列。
	defer s.events.unsubscribe(client)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	// 反向代理（--trusted-proxy 场景）默认会缓冲响应；显式关掉，否则事件会被攒在代理里。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// 先 flush 响应头再写第一帧：浏览器 onopen 立刻触发，而不是等第一条任务事件。
	flusher.Flush()

	if err := writeEvent(w, ServerEvent{Type: EventHello, Time: time.Now().UnixMilli(), State: StateProgress}); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(eventHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			// 客户端断开：立即返回并注销。这是「断开不泄漏 goroutine」的关键分支——
			// 只等 channel 的话，空闲连接上的处理器会一直挂到下一次有事件为止。
			return
		case ev := <-client.ch:
			if err := writeEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// eventTokenAllowed 校验 /events 的 token：请求头优先，其次 ?token=（EventSource 的限制）。
// 没配 token 时 tokenMatches 一律放行（与 tokenMiddleware 同一判定）。
func (s *APIServer) eventTokenAllowed(r *http.Request) bool {
	presented := r.Header.Get("X-Serve-Token")
	if presented == "" {
		presented = r.URL.Query().Get("token")
	}
	return tokenMatches(presented, s.serveToken)
}

func writeEvent(w io.Writer, ev ServerEvent) error {
	frame, err := ev.frame()
	if err != nil {
		return err
	}
	_, err = w.Write(frame)
	return err
}

// publishTaskEvent 把任务的当前状态推成一条事件。
//
// 进度粒度说明：download 层的逐字节进度没有对外 hook（本轮边界限定在 internal/server），
// 所以进度事件发布在服务端**可观测**的边界上——入队、开始执行、拿到元数据、每件产物落盘、
// 终止状态；downloaded 取产物字节数累计（API 的 TotalDownloadedBytes 字段，此前一直是 0）。
// 总量在没有可靠来源时保持 0（未知），页面据此显示「未知」而不是编一个数字。
func (s *APIServer) publishTaskEvent(typ string, task *DownloadTask, state string) {
	if s.events == nil {
		return
	}
	if state == "" {
		state = StateProgress
	}
	speed := task.sampleSpeed(time.Now())
	snap := task.Snapshot()
	s.events.publish(ServerEvent{
		Type:       typ,
		JobID:      snap.JobID,
		Aid:        snap.Aid,
		URL:        snap.URL,
		Title:      snap.Title,
		Status:     snap.Status,
		Percent:    snap.Progress * 100,
		Downloaded: snap.TotalDownloadedBytes,
		Speed:      speed,
		State:      state,
		Error:      snap.ErrorMessage,
	})
}

// onArtifactSaved 记录一件产物落盘：更新任务字节数并推一条进度事件。
// 同一路径重复上报（分片重试 / 断点续传会重复调用 OnSaved）只计一次，不重复计字节。
func (s *APIServer) onArtifactSaved(task *DownloadTask, path string) {
	if !task.AddSavePath(path) {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		task.mu.Lock()
		task.TotalDownloadedBytes += info.Size()
		task.mu.Unlock()
	}
	s.publishTaskEvent(EventTaskProgress, task, StateProgress)
}
