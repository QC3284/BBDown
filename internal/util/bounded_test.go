package util

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestReadAllBounded: an unbounded response body let a broken endpoint or an
// --insecure MITM stream forever and exhaust memory; oversized input must fail
// loudly rather than be silently truncated.
func TestReadAllBounded(t *testing.T) {
	got, err := ReadAllBounded(strings.NewReader("hello"), 16)
	if err != nil || string(got) != "hello" {
		t.Fatalf("small body: %q, %v", got, err)
	}

	// Exactly at the limit is fine.
	if _, err := ReadAllBounded(strings.NewReader(strings.Repeat("x", 16)), 16); err != nil {
		t.Errorf("a body at the limit must pass: %v", err)
	}

	// One byte over must fail, and must not return a truncated body.
	got, err = ReadAllBounded(strings.NewReader(strings.Repeat("x", 17)), 16)
	if err == nil {
		t.Fatal("an oversized body must be rejected")
	}
	if got != nil {
		t.Errorf("an oversized body must not be returned, got %d bytes", len(got))
	}

	// A failing reader propagates its error.
	want := errors.New("boom")
	if _, err := ReadAllBounded(io.MultiReader(strings.NewReader("a"), errReader{want}), 16); !errors.Is(err, want) {
		t.Errorf("reader error = %v, want %v", err, want)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
