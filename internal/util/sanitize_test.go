package util

import (
	"strings"
	"testing"
)

// TestSanitizePathSegment guards the placeholder injection surface: <aid>,
// <cid>, <dfn>, <res>, <fps>, <videoCodecs>, <audioCodecs> and subtitle lan
// come from API responses, so a mirror or --insecure MITM could inject path
// separators or ".." and write outside the target directory (RF-48/58/63/73).
func TestSanitizePathSegment(t *testing.T) {
	// Legitimate values must pass through byte-for-byte: sanitising must never
	// change how a normal download is named.
	for _, s := range []string{"12345678", "BV1xx411c7mD", "1080P 高码率", "avc1.640032", "30000", "zh-CN", "av"} {
		if got := SanitizePathSegment(s); got != s {
			t.Errorf("SanitizePathSegment(%q) = %q, want it unchanged", s, got)
		}
	}

	// Hostile values must no longer be able to escape the target directory.
	for _, s := range []string{
		"../../etc/passwd", "a/b", "a\\b", "..", ".", "x/../../y", "a\x00b", "/abs", "..\\..\\win",
	} {
		got := SanitizePathSegment(s)
		if got == "" || got == "." || got == ".." || strings.ContainsAny(got, "/\\") || strings.ContainsRune(got, 0) {
			t.Errorf("SanitizePathSegment(%q) = %q, still unsafe as a path segment", s, got)
		}
	}

	// The empty value stays empty so callers can distinguish "absent".
	if got := SanitizePathSegment(""); got != "" {
		t.Errorf("SanitizePathSegment(\"\") = %q, want empty", got)
	}
}
