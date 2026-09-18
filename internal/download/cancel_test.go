package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestDownloadFileReturnsPromptlyOnCancel reproduces the reported symptom: the
// download is stalled and the user presses Ctrl+C. Cancellation must return
// quickly instead of waiting for the retry ladder to finish.
func TestDownloadFileReturnsPromptlyOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "1048576")
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // never finishes on its own
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	client := newTestClient()
	cfg := DownloadConfig{MultiThread: false, RetryCount: 3, RetryDelayMs: 1000, Client: client}

	start := time.Now()
	err := DownloadFile(ctx, srv.URL+"/a.m4s", filepath.Join(t.TempDir(), "out.m4s"), cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a cancelled download must return an error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %v: the download does not observe cancellation "+
			"(Ctrl+C appears to hang)", elapsed)
	}
	t.Logf("cancelled after %v with: %v", elapsed, err)
}
