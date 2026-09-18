package cli

import (
	"path/filepath"
	"testing"
)

// TestResolveUnderWorkDir pins how a single-artefact subcommand resolves its
// output: the default name and a relative --output both land under --work-dir,
// while an absolute path is honoured as-is.
func TestResolveUnderWorkDir(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "a.md")
	cases := []struct {
		workDir, path, want string
	}{
		{"", "a.md", "a.md"},
		{"/work", "a.md", filepath.Join("/work", "a.md")},
		{"/work", abs, abs},
		{"/work", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := resolveUnderWorkDir(c.workDir, c.path); got != c.want {
			t.Errorf("resolveUnderWorkDir(%q, %q) = %q, want %q", c.workDir, c.path, got, c.want)
		}
	}

	// A root-relative path carries its own location even without a drive letter:
	// on Windows filepath.IsAbs rejects it, but joining it onto the work dir would
	// silently relocate the product.
	if got := resolveUnderWorkDir("/work", "/rooted/a.md"); got != "/rooted/a.md" {
		t.Errorf("root-relative path = %q, want it left alone", got)
	}
}
