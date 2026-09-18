package workflow

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPathLockSerialisesSamePath: two concurrent tasks writing the same product
// must not overlap, or the loser overwrites the winner's finished file.
func TestPathLockSerialisesSamePath(t *testing.T) {
	var concurrent, maxConcurrent int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockPath("/same/product.mp4")
			n := atomic.AddInt32(&concurrent, 1)
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if n <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
			unlock()
		}()
	}
	wg.Wait()

	if maxConcurrent != 1 {
		t.Errorf("peak concurrency = %d, want 1 (the locks must serialise)", maxConcurrent)
	}
	pathLocksMu.Lock()
	left := len(pathLocks)
	pathLocksMu.Unlock()
	if left != 0 {
		t.Errorf("pathLocks leaked %d entries", left)
	}
}

// TestPathLockIndependentPaths: unrelated products must not block each other, or
// a batch download would be serialised for no reason.
func TestPathLockIndependentPaths(t *testing.T) {
	unlockA := lockPath("/a.mp4")
	done := make(chan struct{})
	go func() {
		unlockB := lockPath("/b.mp4")
		unlockB()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("an unrelated path was blocked by another path's lock")
	}
	unlockA()
}
