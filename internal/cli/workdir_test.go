package cli

import (
	"path/filepath"
	"testing"
)

// TestResolveUnderWorkDir pins how a single-artefact subcommand resolves its
// output: the default name and a relative --output both land under --work-dir,
// while an absolute path is honoured as-is.
func TestResolveUnderWorkDir(t *testing.T) {
	cases := []struct {
		workDir, path, want string
	}{
		{"", "a.md", "a.md"},
		{"/work", "a.md", filepath.Join("/work", "a.md")},
		{"/work", "/abs/a.md", "/abs/a.md"},
		{"/work", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := resolveUnderWorkDir(c.workDir, c.path); got != c.want {
			t.Errorf("resolveUnderWorkDir(%q, %q) = %q, want %q", c.workDir, c.path, got, c.want)
		}
	}
}
