package server

import (
	"net/http"
	"strings"
	"testing"
)

// TestAddTaskOversizedBodyIs413: an oversized body used to be reported as a
// generic 400, so a client could not tell "too big" from "malformed" and had no
// signal to change what it sends.
func TestAddTaskOversizedBodyIs413(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:23333", 1, "", "")
	h := s.buildHandler()

	big := `{"url":"` + strings.Repeat("a", maxRequestBodySize+1024) + `"}`
	rec := do(h, http.MethodPost, "http://127.0.0.1:23333/add-task",
		"127.0.0.1:23333", "", "application/json", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d, want 413", rec.Code)
	}

	// A malformed but small body is still a 400.
	rec = do(h, http.MethodPost, "http://127.0.0.1:23333/add-task",
		"127.0.0.1:23333", "", "application/json", "{")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400", rec.Code)
	}
}
