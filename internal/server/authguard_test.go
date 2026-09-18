package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAuthGuardLocksOutAndResets: repeated failures must lock the client out,
// and a success must clear the counter so a legitimate client is never affected.
func TestAuthGuardLocksOutAndResets(t *testing.T) {
	g := newAuthGuard()
	if _, blocked := g.blocked("10.0.0.1"); blocked {
		t.Fatal("a fresh client must not be blocked")
	}
	for i := 0; i < maxAuthFailures; i++ {
		g.fail("10.0.0.1")
	}
	if _, blocked := g.blocked("10.0.0.1"); !blocked {
		t.Errorf("expected a lockout after %d failures", maxAuthFailures)
	}
	// A different client is unaffected.
	if _, blocked := g.blocked("10.0.0.2"); blocked {
		t.Error("one client's failures must not block another")
	}
	g.reset("10.0.0.1")
	if _, blocked := g.blocked("10.0.0.1"); blocked {
		t.Error("a successful authentication must clear the lockout")
	}
}

// TestAuthGuardStaysBounded: a flood of distinct clients must not grow the map
// without limit — the guard would otherwise become the memory leak it exists to
// prevent (upstream RF-9).
func TestAuthGuardStaysBounded(t *testing.T) {
	g := newAuthGuard()
	for i := 0; i < maxAuthTrackers*3; i++ {
		g.fail(string(rune('a'+i%26)) + "-client-" + time.Now().Format("150405.000000000"))
	}
	g.mu.Lock()
	size := len(g.hits)
	g.mu.Unlock()
	if size > maxAuthTrackers {
		t.Errorf("tracked clients = %d, want <= %d", size, maxAuthTrackers)
	}
}

// TestServeTokenLockoutEndToEnd drives the guard through the real handler.
func TestServeTokenLockoutEndToEnd(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:23333", 1, "s3cret", "")
	h := s.buildHandler()

	attempt := func(token string) *httptest.ResponseRecorder {
		// /health is deliberately unauthenticated (documented), so the guard is
		// exercised through a real API path.
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:23333/get-tasks", nil)
		req.Host = "127.0.0.1:23333"
		if token != "" {
			req.Header.Set("X-Serve-Token", token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if got := attempt("s3cret").Code; got != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", got)
	}
	for i := 0; i < maxAuthFailures; i++ {
		if got := attempt("wrong").Code; got != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, got)
		}
	}
	rec := attempt("wrong")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures = %d, want 429", maxAuthFailures, rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After so clients can back off uniformly")
	}
	// The correct token still works (and clears the counter).
	if got := attempt("s3cret").Code; got != http.StatusOK {
		t.Errorf("valid token while locked out = %d, want 200", got)
	}
	if got := attempt("wrong").Code; got != http.StatusUnauthorized {
		t.Errorf("after a success the counter must be reset, got %d", got)
	}
}
