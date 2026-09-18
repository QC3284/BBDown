package download

import (
	"io"
	"testing"
	"time"
)

// TestStallGuardAbortsIdleRead: a connection that stops producing bytes without
// resetting or EOFing used to block io.Copy forever, hanging the whole task.
func TestStallGuardAbortsIdleRead(t *testing.T) {
	old := downloadStallTimeout
	downloadStallTimeout = 100 * time.Millisecond
	defer func() { downloadStallTimeout = old }()

	pr, pw := io.Pipe()
	defer pw.Close()

	g := newStallGuard(pr, downloadStallTimeout)
	defer g.Stop()

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, g)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a stalled download returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stall guard did not fire: the download hung")
	}
}

// TestStallGuardAllowsSlowButLiveTransfer: the watchdog must reset on every
// byte received, or a slow-but-working download would be killed mid-transfer.
func TestStallGuardAllowsSlowButLiveTransfer(t *testing.T) {
	old := downloadStallTimeout
	downloadStallTimeout = 300 * time.Millisecond
	defer func() { downloadStallTimeout = old }()

	pr, pw := io.Pipe()
	defer pw.Close()

	g := newStallGuard(pr, downloadStallTimeout)
	defer g.Stop()

	go func() {
		for i := 0; i < 5; i++ {
			_, _ = pw.Write([]byte("x"))
			time.Sleep(100 * time.Millisecond) // well under the timeout each time
		}
		_ = pw.Close()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, g)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a live transfer was aborted: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow-but-live transfer hung")
	}
}
