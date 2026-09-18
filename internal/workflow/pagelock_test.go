package workflow

import (
	"testing"
	"time"
)

// TestPageLockIsAcquiredOnce reproduces the deadlock that made retries hang
// forever: the page lock is held until the page finishes, so a second acquire
// from the same goroutine (which is what the retry loop does after a failed
// download) blocked on a lock it already owned. A mutex wait also ignores
// context cancellation, which is why Ctrl+C appeared to do nothing.
func TestPageLockIsAcquiredOnce(t *testing.T) {
	var lock pageLock
	defer lock.unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Both calls must be no-ops after the first acquire.
		lock.acquire("/same/product.mp4")
		lock.acquire("/same/product.mp4")
		lock.acquire("/same/product.mp4")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("acquiring the page lock twice blocked: the retry path would deadlock")
	}

	// Another path must still be able to take its own lock.
	other := make(chan struct{})
	go func() {
		defer close(other)
		unlock := lockPath("/other/product.mp4")
		unlock()
	}()
	select {
	case <-other:
	case <-time.After(2 * time.Second):
		t.Fatal("an unrelated path was blocked")
	}
}

// TestPageLockReleasesOnUnlock: after unlock the path must be free again, or a
// later run of the same page would hang.
func TestPageLockReleasesOnUnlock(t *testing.T) {
	var lock pageLock
	lock.acquire("/product.mp4")
	lock.unlock()

	acquired := make(chan struct{})
	go func() {
		defer close(acquired)
		unlock := lockPath("/product.mp4")
		unlock()
	}()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("the path stayed locked after unlock")
	}
}
