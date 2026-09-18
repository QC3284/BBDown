package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newLoopbackServer builds a server the way the default deployment does: bound
// to loopback with no token configured.
func newLoopbackServer() *APIServer {
	return NewAPIServer("http://127.0.0.1:23333", 2, "", "")
}

func do(h http.Handler, method, target, host, origin, contentType, reqBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(reqBody))
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestBuildHandlerRejectsRebindingHost is the regression for the missing host
// check: with no token configured the handler had no request-level guard at
// all, so a page rebinding its own hostname to 127.0.0.1 could read
// /get-tasks (including absolute SavePaths) from the user's browser.
func TestBuildHandlerRejectsRebindingHost(t *testing.T) {
	h := newLoopbackServer().buildHandler()

	for _, host := range []string{"attacker.example", "attacker.example:23333", "192.168.1.10:23333"} {
		if got := do(h, http.MethodGet, "http://127.0.0.1:23333/health", host, "", "", "").Code; got != http.StatusForbidden {
			t.Errorf("Host %q: status %d, want 403", host, got)
		}
	}
	for _, host := range []string{"127.0.0.1:23333", "localhost:23333", "[::1]:23333"} {
		if got := do(h, http.MethodGet, "http://127.0.0.1:23333/health", host, "", "", "").Code; got != http.StatusOK {
			t.Errorf("Host %q: status %d, want 200", host, got)
		}
	}
}

// TestBuildHandlerRejectsSimpleRequestCSRF covers the cross-origin write path:
// a text/plain POST is a CORS "simple request" delivered without a preflight,
// so Origin and Content-Type must be checked on the write endpoints.
func TestBuildHandlerRejectsSimpleRequestCSRF(t *testing.T) {
	h := newLoopbackServer().buildHandler()
	const target = "http://127.0.0.1:23333/add-task"
	jsonBody := "{\"url\":\"x\"}"

	if got := do(h, http.MethodPost, target, "127.0.0.1:23333", "https://evil.example", "text/plain", jsonBody).Code; got != http.StatusForbidden {
		t.Errorf("cross-origin write: status %d, want 403", got)
	}
	if got := do(h, http.MethodPost, target, "127.0.0.1:23333", "", "text/plain", jsonBody).Code; got != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain write: status %d, want 415", got)
	}
	// A same-origin JSON request must still reach the handler.
	if got := do(h, http.MethodPost, target, "127.0.0.1:23333", "http://127.0.0.1:23333", "application/json", jsonBody).Code; got == http.StatusForbidden || got == http.StatusUnsupportedMediaType {
		t.Errorf("same-origin JSON write was blocked: status %d", got)
	}
	// Read endpoints carry no body and must not be gated on Content-Type.
	if got := do(h, http.MethodGet, "http://127.0.0.1:23333/get-tasks", "127.0.0.1:23333", "", "", "").Code; got != http.StatusOK {
		t.Errorf("GET /get-tasks: status %d, want 200", got)
	}
}

// TestBuildHandlerSecurityHeaders pins the no-store/nosniff hardening: task
// listings expose local absolute paths and must not be cached.
func TestBuildHandlerSecurityHeaders(t *testing.T) {
	rec := do(newLoopbackServer().buildHandler(), http.MethodGet, "http://127.0.0.1:23333/health", "127.0.0.1:23333", "", "", "")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
