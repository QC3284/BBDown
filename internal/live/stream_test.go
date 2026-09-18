package live

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

const partialPayload = "partial-live-content"

// flushThenHold writes a few bytes, flushes them, and holds the connection open
// until the client goes away — a live stream that stops delivering data.
func flushThenHold(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(partialPayload))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	<-r.Context().Done()
}

// TestDownloadToFileKeepsPartialContentOnCancel pins the v1.6.13 alignment fix.
// Cancelling mid-segment used to delete the segment before the cancellation was
// even checked, so a Ctrl+C after minutes of recording threw away everything.
func TestDownloadToFileKeepsPartialContentOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(flushThenHold))
	defer srv.Close()

	old := resolveLive
	resolveLive = func(context.Context, string, *util.HTTPClient) (string, string, string, error) {
		return srv.URL, "title", "uname", nil
	}
	defer func() { resolveLive = old }()

	dir := t.TempDir()
	out := filepath.Join(dir, "room.flv")

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		state LiveRecordResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		state, err := DownloadToFile(ctx, "1", out, nil)
		done <- result{state, err}
	}()

	time.Sleep(150 * time.Millisecond) // let the first bytes land
	cancel()

	select {
	case r := <-done:
		if r.state != LiveSuccess {
			t.Fatalf("DownloadToFile reported %v (err=%v); recorded bytes were discarded", r.state, r.err)
		}
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DownloadToFile did not return after cancellation")
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("recorded output missing: %v", err)
	}
	if string(data) != partialPayload {
		t.Fatalf("recorded %q, want %q", data, partialPayload)
	}
}

// TestDownloadToFileKeepsSegmentsOnTerminalFailure: segments must survive for
// manual recovery instead of being deleted by a blanket RemoveAll of the whole
// .segs root (which also held previously retained sessions).
func TestDownloadToFileKeepsSegmentsOnTerminalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576") // promise far more than we send
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(partialPayload))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	old := resolveLive
	resolveLive = func(context.Context, string, *util.HTTPClient) (string, string, string, error) {
		return srv.URL, "title", "uname", nil
	}
	defer func() { resolveLive = old }()

	dir := t.TempDir()
	out := filepath.Join(dir, "room.flv")
	segRoot := out + ".segs"

	// A previous session already retained segments under the same root.
	retained := filepath.Join(segRoot, "session-20200101_000000")
	if err := os.MkdirAll(retained, 0o755); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(retained, "seg-000.flv")
	if err := os.WriteFile(kept, []byte("earlier-session"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// The connection drops, so the recorder retries and backoffs; the timeout
	// ends the test without waiting for the full backoff schedule.
	_, _ = DownloadToFile(ctx, "1", out, nil)

	if _, err := os.Stat(kept); err != nil {
		t.Errorf("a previously retained session was deleted: %v", err)
	}
}

func TestStreamToFileAbortsOnReadStall(t *testing.T) {
	oldTimeout := readStallTimeout
	readStallTimeout = 150 * time.Millisecond
	defer func() { readStallTimeout = oldTimeout }()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(partialPayload))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // network black hole: no data, no EOF, no reset
	}))
	defer srv.Close()
	defer close(release)

	done := make(chan error, 1)
	go func() {
		_, err := streamToFile(context.Background(), srv.URL, filepath.Join(t.TempDir(), "seg.flv"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a stalled read returned nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("streamToFile hung on a stalled read: the watchdog did not fire")
	}
}

func TestStreamToFileWriteFailureIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(partialPayload))
	}))
	defer srv.Close()

	_, err := streamToFile(context.Background(), srv.URL, filepath.Join(t.TempDir(), "missing-dir", "seg.flv"))
	var werr errLiveWrite
	if !errors.As(err, &werr) {
		t.Fatalf("a local write failure must be reported as errLiveWrite, got %T: %v", err, err)
	}
}

func TestReconnectBackoffGrowsAndCaps(t *testing.T) {
	if got := reconnectBackoff(0); got != reconnectBaseBackoff {
		t.Errorf("first backoff = %v, want %v", got, reconnectBaseBackoff)
	}
	if got := reconnectBackoff(1000); got != reconnectMaxBackoff {
		t.Errorf("late backoff = %v, want %v", got, reconnectMaxBackoff)
	}
}

// TestLiveRecordStateClassification pins the three states: a caller must be able
// to tell "nothing captured" from "the merge failed but segments are on disk",
// which a plain bool could not express.
func TestLiveRecordStateClassification(t *testing.T) {
	if got := stateFor(0); got != LiveNoData {
		t.Errorf("stateFor(0) = %v, want no-data", got)
	}
	if got := stateFor(1024); got != LiveConcatFailedWithSegmentsSaved {
		t.Errorf("stateFor(1024) = %v, want concat-failed-segments-saved", got)
	}

	want := map[LiveRecordResult]string{
		LiveNoData:                        "no-data",
		LiveSuccess:                       "success",
		LiveConcatFailedWithSegmentsSaved: "concat-failed-segments-saved",
	}
	for state, s := range want {
		if got := state.String(); got != s {
			t.Errorf("String() = %q, want %q", got, s)
		}
	}
}
