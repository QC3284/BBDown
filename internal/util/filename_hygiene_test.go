package util

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestGetValidFileNameHygiene covers the two Windows-facing gaps: a trailing dot
// or space is silently dropped by the filesystem (so the file lands under an
// unexpected name), and an unbounded basename can be cut by the filesystem or
// by a mid-rune truncation.
func TestGetValidFileNameHygiene(t *testing.T) {
	if got := GetValidFileName("video.", "_", false); got != "video" {
		t.Errorf("GetValidFileName(\"video.\") = %q, want the trailing dot trimmed", got)
	}
	if got := GetValidFileName("video  ", "_", false); got != "video" {
		t.Errorf("GetValidFileName(\"video  \") = %q, want trailing spaces trimmed", got)
	}
	if got := GetValidFileName("标题.", "_", false); got != "标题" {
		t.Errorf("GetValidFileName(\"标题.\") = %q, want the trailing dot trimmed", got)
	}

	// A host name that is only dots must not collapse into an empty component.
	if got := GetValidFileName("...", "_", false); got == "" {
		t.Error("GetValidFileName(\"...\") = \"\", want a usable fallback name")
	}

	// Long names are capped, counted in runes so UTF-8 stays valid.
	long := strings.Repeat("あ", maxFileNameRunes*2)
	got := GetValidFileName(long, "_", false)
	if len([]rune(got)) > maxFileNameRunes+1 {
		t.Errorf("length = %d runes, want <= %d", len([]rune(got)), maxFileNameRunes+1)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}

	// Reserved names are still handled after trimming (CON. -> CON -> _CON).
	if got := GetValidFileName("CON.", "_", false); got != "_CON" {
		t.Errorf("GetValidFileName(\"CON.\") = %q, want _CON", got)
	}
}
