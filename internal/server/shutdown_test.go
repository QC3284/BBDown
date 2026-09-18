package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestShutdownCancelsRunningTasks is the regression for the shutdown path: the
// wait loop never cancelled anything, so tasks stayed Running, were never
// persisted, and the process exit truncated them after the 30s grace period.
// The execution slot is pre-filled so the task must take the cancellation
// branch deterministically.
func TestShutdownCancelsRunningTasks(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:23333", 1, "", "")
	s.taskFile = filepath.Join(t.TempDir(), "bbdown-tasks.json")
	s.semaphore <- struct{}{} // occupy the only slot; nothing may start

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the server is already shutting down
	s.taskBaseCtx = ctx

	rec := do(s.buildHandler(), http.MethodPost, "http://127.0.0.1:23333/add-task",
		"127.0.0.1:23333", "", "application/json", `{"url":"BV1xx411c7mD"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /add-task = %d, want 202", rec.Code)
	}

	done := make(chan struct{})
	go func() { s.taskWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled task never finished")
	}

	s.mu.Lock()
	finished := snapshotList(s.finishedTasks)
	s.mu.Unlock()
	if len(finished) != 1 {
		t.Fatalf("finished tasks = %d, want 1", len(finished))
	}
	if finished[0].Status != StatusCancelled {
		t.Errorf("task status = %q, want %q", finished[0].Status, StatusCancelled)
	}

	// And it must have reached disk, not just memory.
	data, err := os.ReadFile(s.taskFile)
	if err != nil {
		t.Fatalf("task file was not written: %v", err)
	}
	if !strings.Contains(string(data), finished[0].JobID) {
		t.Errorf("the cancelled task was not persisted: %s", data)
	}
}

func TestMaskTaskErrorHidesLocalPaths(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := maskTaskError("open " + filepath.Join(wd, "BV1xx", "video.mp4") + ": no such file")
	if strings.Contains(got, wd) {
		t.Errorf("the working directory leaked into an API-visible error: %q", got)
	}
	if !strings.Contains(got, "<work-dir>") {
		t.Errorf("expected the working directory to be masked: %q", got)
	}
	if got := maskTaskError(""); got != "" {
		t.Errorf("empty message = %q, want empty", got)
	}
}

// TestPersistFinishedTasksLeavesNoTempFiles covers the shared-temp-name race:
// concurrent writers used one "<file>.tmp" path; each write now uses its own.
func TestPersistFinishedTasksLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewAPIServer("http://127.0.0.1:23333", 2, "", "")
	s.taskFile = filepath.Join(dir, "bbdown-tasks.json")

	for i := 0; i < 4; i++ {
		task := &DownloadTask{JobID: generateJobID(), Status: StatusSucceeded, TaskFinishTime: time.Now().Unix()}
		task.mu = &sync.Mutex{}
		s.finishedTasks = append(s.finishedTasks, task)
	}

	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() { s.persistFinishedTasks(); done <- struct{}{} }()
	}
	for i := 0; i < 4; i++ {
		<-done
	}

	if _, err := os.Stat(s.taskFile); err != nil {
		t.Fatalf("task file missing after concurrent writes: %v", err)
	}
	left, err := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temporary files survived: %v", left)
	}
}
