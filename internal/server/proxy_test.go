package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientKeyTrustsForwardedForOnlyForTrustedProxy: X-Forwarded-For is
// attacker-controlled, so honouring it unconditionally would let one client
// forge unlimited identities and bypass the auth lockout entirely.
func TestClientKeyTrustsForwardedForOnlyForTrustedProxy(t *testing.T) {
	s := NewAPIServer("http://127.0.0.1:23333", 1, "secret", "")

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:23333/get-tasks", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := s.clientKey(req); got != "203.0.113.9" {
		t.Errorf("without --trusted-proxy = %q, want the socket address", got)
	}

	s.SetTrustedProxy("10.0.0.5")
	if got := s.clientKey(req); got != "203.0.113.9" {
		t.Errorf("request not from the proxy = %q, want the socket address", got)
	}

	req.RemoteAddr = "10.0.0.5:443"
	if got := s.clientKey(req); got != "1.2.3.4" {
		t.Errorf("trusted-proxy request = %q, want the forwarded address", got)
	}

	// Only the last hop is trusted: earlier entries are client-supplied.
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")
	if got := s.clientKey(req); got != "198.51.100.7" {
		t.Errorf("multi-hop XFF = %q, want the last entry", got)
	}

	req.Header.Del("X-Forwarded-For")
	if got := s.clientKey(req); got != "10.0.0.5" {
		t.Errorf("missing XFF = %q, want the socket address", got)
	}
}

func TestHostMatches(t *testing.T) {
	if !hostMatches("10.0.0.5", "10.0.0.5") {
		t.Error("plain address must match")
	}
	if !hostMatches("10.0.0.5:8080", "10.0.0.5") {
		t.Error("a configured port must be ignored when comparing")
	}
	if hostMatches("10.0.0.6", "10.0.0.5") {
		t.Error("different addresses must not match")
	}
}
