package server

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAddSavePathIsASet(t *testing.T) {
	task := &DownloadTask{JobID: "j1"}
	task.mu = &sync.Mutex{}

	task.AddSavePath("/out/a.mp4")
	task.AddSavePath("/out/a.mp4") // a retry or resumed page reports it again
	task.AddSavePath("/out/b.mp4")
	task.AddSavePath("") // an unset path must not become an entry

	if got := len(task.Snapshot().SavePaths); got != 2 {
		t.Errorf("SavePaths = %d entries, want 2 (duplicates and the empty path dropped)", got)
	}
}

// TestPersistTrimsByCreationTime: the list is in completion order, so trimming
// the tail kept whichever tasks finished last — which can be the OLDEST created
// ones. Retention must drop the oldest *created* tasks.
func TestPersistTrimsByCreationTime(t *testing.T) {
	dir := t.TempDir()
	s := NewAPIServer("http://127.0.0.1:23333", 1, "", "")
	s.taskFile = filepath.Join(dir, "bbdown-tasks.json")

	// Appended newest-first: a positional trim would keep the oldest 1000.
	for i := 0; i <= maxFinishedTasks; i++ {
		task := &DownloadTask{JobID: generateJobID(), Status: StatusSucceeded}
		task.mu = &sync.Mutex{}
		task.TaskCreateTime = int64(2000 - i) // 2000 down to 1000
		task.TaskFinishTime = time.Now().Unix()
		s.finishedTasks = append(s.finishedTasks, task)
	}

	s.persistFinishedTasks()

	s.mu.Lock()
	n := len(s.finishedTasks)
	oldest := s.finishedTasks[0].TaskCreateTime
	s.mu.Unlock()

	if n != maxFinishedTasks {
		t.Fatalf("in-memory tasks = %d, want %d", n, maxFinishedTasks)
	}
	if oldest != 1001 {
		t.Errorf("oldest surviving create time = %d, want 1001 (only the first created may be dropped)", oldest)
	}
}
