package drm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOverwriteFileZeroFillsWholePayload: the "secure delete" of the temporary
// kid:key file used a hard-coded 64 bytes, so the 65th character (the last hex
// digit of the key) survived on disk after the file was removed.
func TestOverwriteFileZeroFillsWholePayload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bbdown-key.tmp")

	payload := "0123456789abcdef0123456789abcdef:0123456789abcdef0123456789abcdef" // 65 bytes
	if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := overwriteFile(p, len(payload)); err != nil {
		t.Fatalf("overwriteFile: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("size = %d, want %d (the whole payload must be overwritten)", len(got), len(payload))
	}
	for i, b := range got {
		if b != 0 {
			t.Fatalf("byte %d = %q, want a NUL", i, b)
		}
	}

	if err := overwriteFile(filepath.Join(dir, "missing"), 0); err != nil {
		t.Errorf("size 0 = %v, want nil (no-op)", err)
	}
}
