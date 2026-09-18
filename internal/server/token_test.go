package server

import "testing"

// TestTokenMatches pins the constant-time comparison semantics, including the
// no-token-configured case (the guard middleware is what protects that setup).
func TestTokenMatches(t *testing.T) {
	if !tokenMatches("", "") {
		t.Error("an unset token must match anything")
	}
	if !tokenMatches("s3cret", "s3cret") {
		t.Error("equal tokens must match")
	}
	if tokenMatches("s3cre", "s3cret") {
		t.Error("a prefix must not match")
	}
	if tokenMatches("s3cret ", "s3cret") {
		t.Error("trailing whitespace must not match")
	}
	if tokenMatches("", "s3cret") {
		t.Error("an empty presented token must not match a configured one")
	}
}
