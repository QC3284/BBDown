package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func shrinkRetries(t *testing.T) {
	t.Helper()
	oldBackoff, oldRetries := apiRetryBackoff, apiRetries
	apiRetryBackoff = time.Millisecond
	apiRetries = 3
	t.Cleanup(func() { apiRetryBackoff, apiRetries = oldBackoff, oldRetries })
}

// TestGetWebSourceRetriesTransientFailures: a single 5xx used to fail the whole
// API call even though the very next attempt would have succeeded.
func TestGetWebSourceRetriesTransientFailures(t *testing.T) {
	shrinkRetries(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewHTTPClient(nil, func() string { return "" }, nil)
	body, err := c.GetWebSource(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("a transient 5xx must be retried: %v", err)
	}
	if body != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

// TestGetWebSourceDoesNotRetryClientErrors: a 4xx is deterministic; retrying it
// wastes the budget and delays the real error.
func TestGetWebSourceDoesNotRetryClientErrors(t *testing.T) {
	shrinkRetries(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewHTTPClient(nil, func() string { return "" }, nil)
	if _, err := c.GetWebSource(context.Background(), srv.URL); err == nil {
		t.Fatal("a 4xx must be reported")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want exactly 1 (no retry for 4xx)", got)
	}
}

// TestGetWebSourceGivesUpAfterRetries bounds the policy.
func TestGetWebSourceGivesUpAfterRetries(t *testing.T) {
	shrinkRetries(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewHTTPClient(nil, func() string { return "" }, nil)
	if _, err := c.GetWebSource(context.Background(), srv.URL); err == nil {
		t.Fatal("a persistent 5xx must eventually fail")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (apiRetries)", got)
	}
}
