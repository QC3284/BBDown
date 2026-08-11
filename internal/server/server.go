package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// TaskStatus represents the state of a download task.
type TaskStatus string

const (
	StatusQueued    TaskStatus = "queued"
	StatusRunning   TaskStatus = "running"
	StatusSucceeded TaskStatus = "succeeded"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

// DownloadTask tracks a single download operation.
type DownloadTask struct {
	JobID        string     `json:"job_id"`
	Aid          string     `json:"aid"`
	URL          string     `json:"url"`
	Title        string     `json:"title,omitempty"`
	Status       TaskStatus `json:"status"`
	Progress     float64    `json:"progress"`
	ErrorMessage string     `json:"error_message,omitempty"`
	SavePaths    []string   `json:"save_paths,omitempty"`
	CreatedAt    int64      `json:"created_at"`
	FinishedAt   int64      `json:"finished_at,omitempty"`

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

// AddSavePath adds a file path to the task's save list.
func (t *DownloadTask) AddSavePath(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.SavePaths = append(t.SavePaths, path)
}

// SetStatus updates the task status.
func (t *DownloadTask) SetStatus(s TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = s
}

// TaskResponse for add-task endpoint.
type AddTaskResponse struct {
	TaskID string `json:"task_id"`
}

// TaskListResponse wraps running and finished tasks.
type TaskListResponse struct {
	Running  []DownloadTask `json:"running"`
	Finished []DownloadTask `json:"finished"`
}

// APIServer runs BBDown in HTTP API server mode.
type APIServer struct {
	listenURL     string
	maxConcurrent int
	serveToken    string
	notifyWebhook string

	mu             sync.Mutex
	runningTasks   []*DownloadTask
	finishedTasks  []*DownloadTask
	semaphore      chan struct{}
	taskFile       string
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
		taskFile:      "bbdown-tasks.json",
	}
}

// Run starts the API server.
func (s *APIServer) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// Apply token middleware if configured
	var handler http.Handler = mux
	if s.serveToken != "" {
		handler = s.tokenMiddleware(mux)
	}

	mux.HandleFunc("/get-tasks", s.handleGetTasks)
	mux.HandleFunc("/get-tasks/", s.handleGetTasks)
	mux.HandleFunc("/add-task", s.handleAddTask)
	mux.HandleFunc("/cancel/", s.handleCancel)
	mux.HandleFunc("/remove-finished", s.handleRemoveFinished)
	mux.HandleFunc("/remove-finished/", s.handleRemoveFinished)
	mux.HandleFunc("/health", s.handleHealth)

	// Validate listen URL
	if !strings.HasPrefix(s.listenURL, "http://") {
		return fmt.Errorf("%s 不是合法的 http URL，url 示例：http://0.0.0.0:5000", s.listenURL)
	}

	// Security: non-loopback requires token
	host := strings.TrimPrefix(s.listenURL, "http://")
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	if host != "" && host != "0.0.0.0" && !isLoopback(host) && s.serveToken == "" {
		return fmt.Errorf("监听地址 %s 不是回环地址，必须配置 --serve-token 才能启动", s.listenURL)
	}

	util.Log("API服务器已启动: %s", s.listenURL)
	util.Log("最大并发: %d", s.maxConcurrent)

	server := &http.Server{
		Addr:    strings.TrimPrefix(s.listenURL, "http://"),
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	return server.ListenAndServe()
}

func (s *APIServer) tokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		isAPI := strings.HasPrefix(path, "/get-tasks") ||
			strings.HasPrefix(path, "/add-task") ||
			strings.HasPrefix(path, "/cancel") ||
			strings.HasPrefix(path, "/remove-finished")
		if isAPI && r.Header.Get("X-Serve-Token") != s.serveToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *APIServer) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	runningSnap := make([]DownloadTask, len(s.runningTasks))
	for i, t := range s.runningTasks {
		runningSnap[i] = t.Snapshot()
	}
	finishedSnap := make([]DownloadTask, len(s.finishedTasks))
	for i, t := range s.finishedTasks {
		finishedSnap[i] = t.Snapshot()
	}
	s.mu.Unlock()

	// If specific ID requested
	path := strings.TrimPrefix(r.URL.Path, "/get-tasks")
	path = strings.TrimPrefix(path, "/")
	if path != "" {
		for _, t := range runningSnap {
			if t.JobID == path || t.Aid == path || t.URL == path {
				writeJSON(w, http.StatusOK, t)
				return
			}
		}
		for _, t := range finishedSnap {
			if t.JobID == path || t.Aid == path || t.URL == path {
				writeJSON(w, http.StatusOK, t)
				return
			}
		}
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	// Return all tasks
	resp := TaskListResponse{Running: runningSnap, Finished: finishedSnap}
	writeJSON(w, http.StatusOK, resp)
}

func (s *APIServer) handleAddTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, `{"error":"invalid request body, 'url' required"}`, http.StatusBadRequest)
		return
	}

	// Create task
	jobID := generateJobID()
	task := &DownloadTask{
		JobID:     jobID,
		Aid:       req.URL,
		URL:       req.URL,
		Status:    StatusQueued,
		CreatedAt: time.Now().Unix(),
		mu:        &sync.Mutex{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	task.cancelFn = cancel

	s.mu.Lock()
	s.runningTasks = append(s.runningTasks, task)
	s.mu.Unlock()

	// Process task asynchronously
	go s.processTask(ctx, task, req.URL)

	writeJSON(w, http.StatusAccepted, AddTaskResponse{TaskID: jobID})
}

func (s *APIServer) processTask(ctx context.Context, task *DownloadTask, url string) {
	// Acquire semaphore slot
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		task.SetStatus(StatusCancelled)
		s.moveToFinished(task)
		return
	}

	task.SetStatus(StatusRunning)

	// Simulate download work (placeholder — real implementation calls workflow.Run)
	util.Log("处理任务 %s: %s", task.JobID, url)
	time.Sleep(2 * time.Second) // placeholder

	// Check cancellation
	select {
	case <-ctx.Done():
		task.SetStatus(StatusCancelled)
		s.moveToFinished(task)
		return
	default:
	}

	// On success
	task.SetStatus(StatusSucceeded)
	task.FinishedAt = time.Now().Unix()
	task.Progress = 1.0
	s.moveToFinished(task)

	if s.notifyWebhook != "" {
		s.sendCallback(task)
	}
}

func (s *APIServer) moveToFinished(task *DownloadTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove from running
	for i, t := range s.runningTasks {
		if t.JobID == task.JobID {
			s.runningTasks = append(s.runningTasks[:i], s.runningTasks[i+1:]...)
			break
		}
	}
	s.finishedTasks = append(s.finishedTasks, task)

	// Trim finished list (keep last 1000)
	if len(s.finishedTasks) > 1000 {
		s.finishedTasks = s.finishedTasks[len(s.finishedTasks)-1000:]
	}
}

func (s *APIServer) handleCancel(w http.ResponseWriter, r *http.Request) {
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
	id := strings.TrimPrefix(r.URL.Path, "/remove-finished/")

	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || id == "remove-finished" {
		// Remove all finished
		s.finishedTasks = nil
	} else {
		// Remove specific
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

func (s *APIServer) sendCallback(task *DownloadTask) {
	snapshot := task.Snapshot()
	body, _ := json.Marshal(snapshot)

	req, err := http.NewRequest("POST", s.notifyWebhook, strings.NewReader(string(body)))
	if err != nil {
		util.LogDebug("回调失败: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		util.LogDebug("回调失败: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		util.LogDebug("回调返回 HTTP %d", resp.StatusCode)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func generateJobID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
