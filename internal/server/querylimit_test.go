package server

import (
	"net/http"
	"testing"
)

// TestGetTasksQueryConcurrencyLimited: each listing walks the whole finished
// slice and returns absolute save paths, so parallel queries must be bounded
// instead of piling up (upstream D8).
func TestGetTasksQueryConcurrencyLimited(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:23333", 1, "", "")
	h := s.buildHandler()
	const target = "http://127.0.0.1:23333/get-tasks"

	// Occupy every slot: the next query is rejected rather than queued.
	for i := 0; i < cap(s.queryLimiter); i++ {
		s.queryLimiter <- struct{}{}
	}
	rec := do(h, http.MethodGet, target, "127.0.0.1:23333", "", "", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("with all slots busy = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After")
	}

	// One slot free: the same request succeeds and gives the slot back.
	<-s.queryLimiter
	if rec := do(h, http.MethodGet, target, "127.0.0.1:23333", "", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("with a free slot = %d, want 200", rec.Code)
	}
	if got, want := len(s.queryLimiter), cap(s.queryLimiter)-1; got != want {
		t.Errorf("slots still held = %d, want %d (the request must release its slot)", got, want)
	}
}
