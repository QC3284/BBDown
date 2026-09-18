package cli

import "testing"

// TestSubCheckResult pins the exit status of "sub check": it ended with an
// unconditional "return nil", so a run where every video failed still exited 0
// and scripts could not detect it.
func TestSubCheckResult(t *testing.T) {
	if err := subCheckResult(false, 0); err != nil {
		t.Errorf("a clean run must succeed, got %v", err)
	}
	if err := subCheckResult(false, 3); err == nil {
		t.Error("partial failures must not report success")
	}
	// A user cancellation returns 0, the documented behaviour for subcommands.
	if err := subCheckResult(true, 5); err != nil {
		t.Errorf("cancellation must return nil, got %v", err)
	}
}
