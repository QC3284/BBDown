package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"crypto/subtle"
	"errors"
	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/QC3284/BBDown/internal/workflow"
	"sort"
)

// TaskStatus represents the state of a download task (PascalCase values,
// matching the upstream API contract).
type TaskStatus string

const (
	StatusQueued    TaskStatus = "Queued"
	StatusRunning   TaskStatus = "Running"
	StatusSucceeded TaskStatus = "Succeeded"
	StatusFailed    TaskStatus = "Failed"
	StatusCancelled TaskStatus = "Cancelled"
)

const (
	maxFinishedTasks   = 1000
	finishedRetention  = 30 * 24 * time.Hour
	callbackTimeout    = 2 * time.Minute
	maxRequestBodySize = 64 << 10 // 64KB
)

// DownloadTask tracks a single download operation (upstream JSON contract).
type DownloadTask struct {
	JobID                string     `json:"JobId"`
	Aid                  string     `json:"Aid"`
	URL                  string     `json:"Url"`
	TaskCreateTime       int64      `json:"TaskCreateTime"`
	Title                string     `json:"Title,omitempty"`
	Pic                  string     `json:"Pic,omitempty"`
	VideoPubTime         int64      `json:"VideoPubTime,omitempty"`
	TaskFinishTime       int64      `json:"TaskFinishTime,omitempty"`
	Progress             float64    `json:"Progress"`
	DownloadSpeed        string     `json:"DownloadSpeed,omitempty"`
	TotalDownloadedBytes int64      `json:"TotalDownloadedBytes"`
	IsSuccessful         bool       `json:"IsSuccessful"`
	Status               TaskStatus `json:"Status"`
	ErrorMessage         string     `json:"ErrorMessage,omitempty"`
	SavePaths            []string   `json:"SavePaths,omitempty"`

	cancelFn context.CancelFunc
	mu       *sync.Mutex
}

// Snapshot returns a thread-safe copy of the task.
func (t *DownloadTask) Snapshot() DownloadTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := *t
	cp.SavePaths = make([]string, len(t.SavePaths))
	copy(cp.SavePaths, t.SavePaths)
	return cp
}

// AddSavePath adds a file path to the task save list.
func (t *DownloadTask) AddSavePath(path string) {
	if path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// The list is a set: a resumed page or a retry can report the same artefact
	// twice, and clients should not see duplicate SavePaths (upstream dedupes).
	for _, p := range t.SavePaths {
		if p == path {
			return
		}
	}
	t.SavePaths = append(t.SavePaths, path)
}

// SetStatus updates the task status and derives IsSuccessful (upstream).
func (t *DownloadTask) SetStatus(s TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = s
	t.IsSuccessful = s == StatusSucceeded
}

// AddTaskResponse for the add-task endpoint (upstream {"TaskId": ...}).
type AddTaskResponse struct {
	TaskID string `json:"TaskId"`
}

// TaskListResponse wraps running and finished tasks.
type TaskListResponse struct {
	Running  []DownloadTask `json:"Running"`
	Finished []DownloadTask `json:"Finished"`
}

// APIServer runs BBDown in HTTP API server mode.
type APIServer struct {
	listenURL     string
	maxConcurrent int
	serveToken    string
	notifyWebhook string

	mu            sync.Mutex
	runningTasks  []*DownloadTask
	finishedTasks []*DownloadTask
	semaphore     chan struct{} // execution concurrency (maxConcurrent)
	acceptLimiter chan struct{} // pending-queue cap (maxConcurrent * 9)
	taskFile      string
	taskWG        sync.WaitGroup
	auth          *authGuard
	trustedProxy  string
	queryLimiter  chan struct{} // bounds concurrent /get-tasks queries

	// persistMu serialises bbdown-tasks.json writes: several tasks finish
	// concurrently and each calls persistFinishedTasks.
	persistMu sync.Mutex
	// taskBaseCtx is the parent of every task context, so a shutdown can cancel
	// in-flight downloads instead of truncating them at process exit.
	taskBaseCtx context.Context
}

// NewAPIServer creates a new API server instance.
func NewAPIServer(listenURL string, maxConcurrent int, serveToken, notifyWebhook string) *APIServer {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &APIServer{
		listenURL:     listenURL,
		maxConcurrent: maxConcurrent,
		serveToken:    serveToken,
		notifyWebhook: notifyWebhook,
		semaphore:     make(chan struct{}, maxConcurrent),
		acceptLimiter: make(chan struct{}, maxConcurrent*9),
		taskFile:      "bbdown-tasks.json",
		auth:          newAuthGuard(),
		queryLimiter:  make(chan struct{}, maxConcurrentQueries),
	}
}

// validateListenURL enforces the listen-address security rules (upstream
// ServeCommand + Run double check): http scheme only; non-loopback hosts
// (0.0.0.0/::/interface IPs included) require --serve-token.
func validateListenURL(listenURL, serveToken string) error {
	if !strings.HasPrefix(listenURL, "http://") {
		return fmt.Errorf("%s 不是合法的 http URL，url 示例：http://0.0.0.0:5000", listenURL)
	}
	host := hostOfListenURL(listenURL)
	if !isLoopbackHost(host) && serveToken == "" {
		return fmt.Errorf("监听地址 %s 不是回环地址，必须配置 --serve-token 才能启动", listenURL)
	}
	return nil
}

// Run starts the API server.
// buildHandler wires the API routes plus the token and guard middlewares. It is
// a method so tests can assert the guards are actually installed — the exact
// regression that left the default loopback deployment (no token) with no host
// check and no cross-origin protection at all.
func (s *APIServer) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/get-tasks", s.handleGetTasks)
	mux.HandleFunc("/get-tasks/", s.handleGetTasks)
	mux.HandleFunc("/add-task", s.handleAddTask)
	mux.HandleFunc("/cancel/", s.handleCancel)
	mux.HandleFunc("/remove-finished", s.handleRemoveFinished)
	mux.HandleFunc("/remove-finished/", s.handleRemoveFinished)
	mux.HandleFunc("/health", s.handleHealth)

	var handler http.Handler = mux
	if s.serveToken != "" {
		handler = s.tokenMiddleware(handler)
	}
	// The guard is installed unconditionally: the default loopback deployment
	// has no token, and without it a browser could reach the API through DNS
	// rebinding or a cross-origin "simple request".
	return s.guardMiddleware(handler)
}

func (s *APIServer) Run(ctx context.Context) error {
	if err := validateListenURL(s.listenURL, s.serveToken); err != nil {
		return err
	}

	s.loadFinishedTasks()

	taskBaseCtx, taskCancelAll := context.WithCancel(ctx)
	s.mu.Lock()
	s.taskBaseCtx = taskBaseCtx
	s.mu.Unlock()
	defer taskCancelAll()

	util.Log("API服务器已启动: %s", s.listenURL)
	util.Log("最大并发: %d", s.maxConcurrent)

	server := &http.Server{
		Addr:              strings.TrimPrefix(s.listenURL, "http://"),
		Handler:           s.buildHandler(),
		ReadHeaderTimeout: 30 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-ctx.Done()
		// Cancel in-flight tasks first. Waiting for them without cancelling meant
		// they never reached Cancelled and never persisted, and the process exit
		// truncated them after the 30s grace period.
		taskCancelAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { s.taskWG.Wait(); close(done) }()
		select {
		case <-done:
		case <-shutdownCtx.Done():
			util.LogWarn("等待后台任务超时，强制退出")
		}
		// Persist whatever finished, including tasks cancelled above whose own
		// deferred write may not have run yet.
		s.persistFinishedTasks()
		server.Shutdown(context.Background())
	}()

	return server.ListenAndServe()
}

func hostOfListenURL(listenURL string) string {
	u, err := url.Parse(listenURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isLoopbackHost mirrors upstream IsLoopbackListenAddress: only localhost and
// IP.IsLoopback count as loopback; 0.0.0.0/::/interface IPs are NOT loopback.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// Auth brute-force guard (upstream RF-9/RF-74): without it an unauthenticated
// client may retry the serve token indefinitely, and an unbounded per-client map
// would itself become the memory leak.
const (
	maxAuthFailures   = 10
	authLockoutWindow = 5 * time.Minute
	maxAuthTrackers   = 1000
)

type authFailure struct {
	count int
	until time.Time
}

// authGuard tracks failed token attempts per client, with a hard cap on the
// number of tracked clients.
type authGuard struct {
	mu   sync.Mutex
	hits map[string]*authFailure
}

func newAuthGuard() *authGuard {
	return &authGuard{hits: map[string]*authFailure{}}
}

// blocked reports whether the client must wait before another attempt.
func (g *authGuard) blocked(client string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.hits[client]
	if !ok || e.count < maxAuthFailures || time.Now().After(e.until) {
		return 0, false
	}
	return time.Until(e.until), true
}

// fail records one failed attempt and re-arms the lockout window.
func (g *authGuard) fail(client string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneLocked()
	e, ok := g.hits[client]
	if !ok || time.Now().After(e.until) {
		e = &authFailure{}
		g.hits[client] = e
	}
	e.count++
	e.until = time.Now().Add(authLockoutWindow)
}

// reset clears the counter after a successful authentication.
func (g *authGuard) reset(client string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.hits, client)
}

// pruneLocked keeps the map bounded: expired entries go first, and if that is
// not enough one arbitrary entry is dropped so a flood of distinct clients
// cannot grow it without limit.
func (g *authGuard) pruneLocked() {
	if len(g.hits) < maxAuthTrackers {
		return
	}
	now := time.Now()
	for k, v := range g.hits {
		if now.After(v.until) {
			delete(g.hits, k)
		}
	}
	for k := range g.hits {
		if len(g.hits) < maxAuthTrackers {
			break
		}
		delete(g.hits, k)
	}
}

// SetTrustedProxy marks a reverse proxy whose X-Forwarded-For header may be
// believed when identifying clients.
func (s *APIServer) SetTrustedProxy(host string) { s.trustedProxy = strings.TrimSpace(host) }

// clientKey identifies the caller for the auth guard. RemoteAddr is authoritative
// unless the request arrived from an explicitly trusted proxy: without
// --trusted-proxy the XFF header is attacker-controlled and one client could
// otherwise forge unlimited identities and bypass the lockout.
func (s *APIServer) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.trustedProxy == "" || !hostMatches(s.trustedProxy, host) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
		return last
	}
	return host
}

// hostMatches reports whether a client address is the configured trusted proxy
// (an IP or hostname, optionally with a port).
func hostMatches(trusted, host string) bool {
	if h, _, err := net.SplitHostPort(trusted); err == nil {
		trusted = h
	}
	return strings.EqualFold(trusted, host)
}

// tokenMatches compares the presented serve token in constant time. A plain
// "!=" leaks the shared secret through response timing, which is measurable
// across a loopback or LAN connection.
func tokenMatches(presented, expected string) bool {
	if expected == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

// tokenMiddleware guards API paths with the X-Serve-Token header, using
// path-segment boundary matching (upstream StartsWithSegments).
func (s *APIServer) tokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		isAPI := segmentHasPrefix(path, "/get-tasks") ||
			segmentHasPrefix(path, "/add-task") ||
			segmentHasPrefix(path, "/cancel") ||
			segmentHasPrefix(path, "/remove-finished")
		if isAPI && !tokenMatches(r.Header.Get("X-Serve-Token"), s.serveToken) {
			client := s.clientKey(r)
			if retry, blocked := s.auth.blocked(client); blocked {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retry.Seconds())+1))
				http.Error(w, `{"error":"too many failed attempts"}`, http.StatusTooManyRequests)
				return
			}
			s.auth.fail(client)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if isAPI && s.serveToken != "" {
			s.auth.reset(s.clientKey(r))
		}
		next.ServeHTTP(w, r)
	})
}

func segmentHasPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// guardMiddleware enforces request-level boundaries that must hold even when no
// token is configured. Two independent holes are closed here (upstream RF-15
// DNS rebinding and the v1.6.14 CSRF fix):
//
//   - Host: a public page can resolve its own hostname to 127.0.0.1 so the
//     browser sends the request to this loopback server. Requiring the Host
//     header to name a loopback address blocks that without affecting LAN
//     deployments (which require a token and bind a non-loopback address).
//   - Origin/Content-Type: a cross-origin fetch with Content-Type text/plain
//     is a CORS "simple request" sent without a preflight, which would let any
//     web page drive /add-task and /cancel.
func (s *APIServer) guardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")

		if isLoopbackHost(hostOfListenURL(s.listenURL)) && !s.hostAllowed(r.Host) {
			http.Error(w, `{"error":"invalid host"}`, http.StatusForbidden)
			return
		}
		if isWriteEndpoint(r.URL.Path) {
			if !s.originAllowed(r) {
				http.Error(w, `{"error":"cross-origin request rejected"}`, http.StatusForbidden)
				return
			}
			if !isJSONContentType(r) {
				http.Error(w, `{"error":"content-type must be application/json"}`, http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether a Host header may be served. Only loopback names
// and the configured listen host are accepted.
func (s *APIServer) hostAllowed(hostHeader string) bool {
	host := strings.ToLower(strings.TrimSpace(hostHeader))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" || isLoopbackHost(host) {
		return true
	}
	return host == strings.ToLower(hostOfListenURL(s.listenURL))
}

// originAllowed accepts requests without an Origin header (CLI clients) and
// same-origin browser requests only.
func (s *APIServer) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func isJSONContentType(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	return strings.HasPrefix(ct, "application/json")
}

func isWriteEndpoint(path string) bool {
	return segmentHasPrefix(path, "/add-task") ||
		segmentHasPrefix(path, "/cancel") ||
		segmentHasPrefix(path, "/remove-finished")
}

// maxConcurrentQueries caps simultaneous /get-tasks requests.
const maxConcurrentQueries = 32

func (s *APIServer) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	// Bound concurrent queries: every listing walks the whole finished-task slice
	// and each response exposes absolute save paths, so unbounded parallelism is
	// both a CPU/memory amplifier and an information-disclosure one.
	select {
	case s.queryLimiter <- struct{}{}:
		defer func() { <-s.queryLimiter }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, `{"error":"too many concurrent queries"}`, http.StatusTooManyRequests)
		return
	}
	s.mu.Lock()
	runningSnap := snapshotList(s.runningTasks)
	finishedSnap := snapshotList(s.finishedTasks)
	s.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/get-tasks")
	path = strings.Trim(path, "/")
	switch path {
	case "":
		writeJSON(w, http.StatusOK, TaskListResponse{Running: runningSnap, Finished: finishedSnap})
		return
	case "running":
		writeJSON(w, http.StatusOK, runningSnap)
		return
	case "finished":
		writeJSON(w, http.StatusOK, finishedSnap)
		return
	}

	// Specific ID lookup: finished first (upstream: avoids same-name drift).
	for _, t := range finishedSnap {
		if t.JobID == path || t.Aid == path || t.URL == path {
			writeJSON(w, http.StatusOK, t)
			return
		}
	}
	for _, t := range runningSnap {
		if t.JobID == path || t.Aid == path || t.URL == path {
			writeJSON(w, http.StatusOK, t)
			return
		}
	}
	http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
}

func snapshotList(tasks []*DownloadTask) []DownloadTask {
	out := make([]DownloadTask, len(tasks))
	for i, t := range tasks {
		out[i] = t.Snapshot()
	}
	return out
}

func (s *APIServer) handleAddTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// An oversized body is a 413, not a generic 400: clients need to tell
		// "too big" from "malformed" to decide whether to retry.
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, `{"error":"invalid request body, 'url' required"}`, http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, `{"error":"invalid request body, 'url' required"}`, http.StatusBadRequest)
		return
	}

	// Accept-queue limit: 429 when the pending queue is full (upstream).
	select {
	case s.acceptLimiter <- struct{}{}:
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, `{"error":"任务队列已满，请稍后再试"}`, http.StatusTooManyRequests)
		return
	}

	task := &DownloadTask{
		JobID:          generateJobID(),
		Aid:            req.URL,
		URL:            req.URL,
		Status:         StatusQueued,
		TaskCreateTime: time.Now().Unix(),
		mu:             &sync.Mutex{},
	}
	base := s.taskBaseCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	task.cancelFn = cancel

	s.mu.Lock()
	s.runningTasks = append(s.runningTasks, task)
	s.mu.Unlock()

	s.taskWG.Add(1)
	go s.processTask(ctx, task, req.URL)

	writeJSON(w, http.StatusAccepted, AddTaskResponse{TaskID: task.JobID})
}

// processTask runs a real download via the workflow (upstream
// ProcessDownloadTaskAsync): parse URL → fetch metadata → download pages.
func (s *APIServer) processTask(ctx context.Context, task *DownloadTask, url string) {
	defer s.taskWG.Done()
	defer func() { <-s.acceptLimiter }()
	defer s.persistFinishedTasks()

	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		task.SetStatus(StatusCancelled)
		s.finishTask(task, "")
		s.sendCallback(task)
		return
	}

	util.Log("处理任务 %s: %s", task.JobID, url)
	client := util.NewHTTPClient(
		func() bool { return false },
		func() string { return "" },
		func(format string, args ...interface{}) { util.LogDebug(format, args...) },
	)

	// Resolve the URL up front so failures land in the task state.
	resolved, err := workflow.ResolveURL(ctx, client, url)
	if err != nil {
		task.SetStatus(StatusFailed)
		s.finishTask(task, err.Error())
		s.sendCallback(task)
		return
	}
	if resolved == "" {
		task.SetStatus(StatusFailed)
		s.finishTask(task, "无法解析目标 URL")
		s.sendCallback(task)
		return
	}
	task.mu.Lock()
	task.Aid = resolved
	task.mu.Unlock()

	cfg := config.DefaultMyOption()
	cfg.URL = url
	wf := workflow.New(cfg, client)
	wf.MetaHandler = func(v *entity.VInfo) {
		task.mu.Lock()
		task.Title = v.Title
		task.Pic = v.Pic
		task.VideoPubTime = v.PubTime
		task.mu.Unlock()
	}
	wf.OnSaved = func(path string) { task.AddSavePath(path) }

	task.SetStatus(StatusRunning)
	err = wf.Run(ctx)
	if err != nil {
		status, msg := classifyTaskCancellation(ctx, err)
		task.SetStatus(status)
		s.finishTask(task, msg)
		s.sendCallback(task)
		return
	}

	task.SetStatus(StatusSucceeded)
	task.mu.Lock()
	task.TaskFinishTime = time.Now().Unix()
	task.Progress = 1.0
	task.mu.Unlock()
	s.finishTask(task, "")
	s.sendCallback(task)
}

// maskTaskError removes local absolute paths from an error message before it is
// served to API clients: a failure such as
// "open /home/user/Downloads/BV1xx/...mp4: no such file" leaks the server's
// directory layout to any caller that can reach /get-tasks.
func maskTaskError(msg string) string {
	if msg == "" {
		return msg
	}
	if dir := util.ExecutableDir(); len(dir) > 1 {
		msg = strings.ReplaceAll(msg, dir, "<app-dir>")
	}
	if wd, err := os.Getwd(); err == nil && len(wd) > 1 {
		msg = strings.ReplaceAll(msg, wd, "<work-dir>")
	}
	return util.SanitizeLogString(msg)
}

// finishTask moves a task to the finished list and sets its terminal fields.
// classifyTaskCancellation 对应上游 BBDownApiServer.ClassifyCancellation：
// 只有「任务确实被取消」（ctx 是 Canceled，而非 DeadlineExceeded）才算 Cancelled 并给出
// 统一文案「已取消」；HttpClient 超时抛出的取消类错误其 ctx 并未取消，必须归为 Failed——
// 否则任务被误标「已取消」，把真实失败原因掩盖成用户操作。
func classifyTaskCancellation(ctx context.Context, err error) (TaskStatus, string) {
	if ctx.Err() == context.Canceled {
		return StatusCancelled, "已取消"
	}
	if err != nil {
		return StatusFailed, err.Error()
	}
	return StatusFailed, ""
}

func (s *APIServer) finishTask(task *DownloadTask, errMsg string) {
	task.mu.Lock()
	if task.TaskFinishTime == 0 {
		task.TaskFinishTime = time.Now().Unix()
	}
	if errMsg != "" {
		task.ErrorMessage = maskTaskError(errMsg)
	}
	task.mu.Unlock()
	if task.cancelFn != nil {
		task.cancelFn()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.runningTasks {
		if t.JobID == task.JobID {
			s.runningTasks = append(s.runningTasks[:i], s.runningTasks[i+1:]...)
			break
		}
	}
	s.finishedTasks = append(s.finishedTasks, task)
}

func (s *APIServer) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/cancel/")
	if id == "" {
		http.Error(w, `{"error":"task id required"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.runningTasks {
		if t.JobID == id || t.Aid == id || t.URL == id {
			if t.cancelFn != nil {
				t.cancelFn()
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
			return
		}
	}
	http.Error(w, `{"error":"task not found or already finished"}`, http.StatusNotFound)
}

func (s *APIServer) handleRemoveFinished(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/remove-finished/")
	id = strings.Trim(id, "/")

	s.mu.Lock()
	defer s.mu.Unlock()

	switch id {
	case "":
		s.finishedTasks = nil
	case "failed":
		out := s.finishedTasks[:0]
		for _, t := range s.finishedTasks {
			if t.Snapshot().Status != StatusFailed {
				out = append(out, t)
			}
		}
		s.finishedTasks = out
	default:
		for i, t := range s.finishedTasks {
			if t.JobID == id || t.Aid == id || t.URL == id {
				s.finishedTasks = append(s.finishedTasks[:i], s.finishedTasks[i+1:]...)
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sendCallback posts the task snapshot to the webhook on EVERY terminal state
// (upstream), with the full SSRF guard: literal-IP and DNS branches are checked
// with the same rules as upstream, the connection is dialed to a validated IP,
// redirects are disabled and the timeout is 2 minutes.
func (s *APIServer) sendCallback(task *DownloadTask) {
	if s.notifyWebhook == "" {
		return
	}
	u, err := url.Parse(s.notifyWebhook)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		util.LogWarn("回调地址不合法，已跳过: %s", s.notifyWebhook)
		return
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// Resolve/validate the dial target (upstream SendCallbackAsync).
	var target net.IP
	if literal := net.ParseIP(host); literal != nil {
		literal = normalizeMappedIP(literal)
		if isUnsafeLiteralIP(literal) {
			util.LogWarn("回调地址是敏感字面 IP，已跳过本次回调: %s", s.notifyWebhook)
			return
		}
		target = literal
	} else {
		if strings.EqualFold(host, "localhost") {
			util.LogWarn("回调地址不安全（内网地址），已跳过: %s", s.notifyWebhook)
			return
		}
		addrs, err := dnsLookupIP(context.Background(), "ip", host)
		if err != nil {
			util.LogWarn("回调域名无法解析，已跳过: %s", s.notifyWebhook)
			return
		}
		var chosen net.IP
		for _, a := range addrs {
			a = normalizeMappedIP(a)
			if isBlockedAddress(a) {
				util.LogWarn("回调地址不安全（内网地址），已跳过: %s", s.notifyWebhook)
				return
			}
			if chosen == nil {
				chosen = a
			}
		}
		target = chosen
	}

	snapshot := task.Snapshot()
	body, _ := json.Marshal(snapshot)

	ctx, cancel := context.WithTimeout(context.Background(), callbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.notifyWebhook, strings.NewReader(string(body)))
	if err != nil {
		util.LogDebug("回调失败: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	dialAddr := net.JoinHostPort(target.String(), port)
	client := &http.Client{
		Timeout: callbackTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // no redirects
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Dial the VALIDATED IP: TLS SNI and the Host header still come
				// from the original URL (upstream ConnectCallback).
				var d net.Dialer
				return d.DialContext(ctx, network, dialAddr)
			},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		util.LogDebug("回调失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		util.LogDebug("回调返回 HTTP %d", resp.StatusCode)
	}
}

// isSafeCallbackURL validates a callback URL without network I/O for the literal
// branch; the domain branch is checked at send time (upstream IsSafeCallbackUrl).
func isSafeCallbackURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	if literal := net.ParseIP(host); literal != nil {
		literal = normalizeMappedIP(literal)
		return !isUnsafeLiteralIP(literal)
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	addrs, err := dnsLookupIP(context.Background(), "ip", host)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if isBlockedAddress(normalizeMappedIP(a)) {
			return false
		}
	}
	return true
}

// dnsLookupIP is the DNS resolver used by the callback SSRF checks; it is a
// variable so tests can stub it (production uses net.DefaultResolver).
var dnsLookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, network, host)
}

// normalizeMappedIP maps IPv4-mapped IPv6 addresses back to IPv4 (upstream).
func normalizeMappedIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// isUnsafeLiteralIP blocks loopback / link-local / 169.254/16 / unspecified for
// EXPLICITLY configured literal IPs (upstream: RFC1918 literals are allowed —
// no DNS rebinding is possible for literal addresses).
func isUnsafeLiteralIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 169 && v4[1] == 254 // 169.254.0.0/16 cloud metadata
	}
	return false
}

// isBlockedAddress blocks loopback / link-local / 169.254/16 / RFC1918 / CGNAT
// 100.64/10 / IPv6 ULA fc00::/7 (upstream IsBlockedAddress, domain branch).
func isBlockedAddress(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 169.254.0.0/16 cloud metadata
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
		// RFC1918
		if v4[0] == 10 {
			return true
		}
		if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
			return true
		}
		if v4[0] == 192 && v4[1] == 168 {
			return true
		}
		// CGNAT 100.64.0.0/10
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		return false
	}
	// IPv6 ULA fc00::/7
	return len(ip) == 16 && (ip[0]&0xfe) == 0xfc
}

// ---- Finished-task persistence (upstream bbdown-tasks.json) ----

func (s *APIServer) persistFinishedTasks() {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	list := snapshotList(s.finishedTasks)
	// The in-memory list is what /get-tasks serves and it used to grow without
	// bound for the life of the process; apply the same cap here (only the
	// oldest entries are dropped, so the remaining pointers stay valid).
	if len(s.finishedTasks) > maxFinishedTasks {
		// Retention is keyed on creation time, not on list position: the slice is in
		// completion order, so dropping the head would discard a task that was
		// created earlier but happened to finish later (upstream sorts by
		// TaskCreateTime before trimming).
		byCreate := append([]*DownloadTask(nil), s.finishedTasks...)
		sort.SliceStable(byCreate, func(i, j int) bool { return byCreate[i].TaskCreateTime < byCreate[j].TaskCreateTime })
		s.finishedTasks = byCreate[len(byCreate)-maxFinishedTasks:]
	}
	s.mu.Unlock()

	// Retention: drop entries created more than 30 days ago, keep the newest 1000.
	// Both bounds are keyed on creation time for the same reason as above.
	cutoff := time.Now().Add(-finishedRetention).Unix()
	kept := list[:0]
	for _, t := range list {
		if t.TaskCreateTime >= cutoff {
			kept = append(kept, t)
		}
	}
	if len(kept) > maxFinishedTasks {
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].TaskCreateTime < kept[j].TaskCreateTime })
		kept = kept[len(kept)-maxFinishedTasks:]
	}

	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return
	}
	// A unique temp name: a shared "<file>.tmp" let two concurrent writers race
	// onto the same path and rename a half-written file into place.
	tmp := s.taskFile + ".tmp-" + generateJobID()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, s.taskFile); err != nil {
		os.Remove(tmp)
	}
}

func (s *APIServer) loadFinishedTasks() {
	data, err := os.ReadFile(s.taskFile)
	if err != nil {
		return
	}
	var list []DownloadTask
	if err := json.Unmarshal(data, &list); err != nil {
		util.LogDebug("读取任务持久化文件失败（已忽略）: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range list {
		t := list[i]
		t.mu = &sync.Mutex{}
		s.finishedTasks = append(s.finishedTasks, &t)
	}
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// generateJobID returns a 32-char hex GUID (upstream Guid.NewGuid().ToString("N")).
func generateJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
