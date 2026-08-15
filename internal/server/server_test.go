package server

import (
	"net"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost":    true,
		"LOCALHOST":    true,
		"127.0.0.1":    true,
		"::1":          true,
		"0.0.0.0":      false, // must NOT be exempt (upstream)
		"::":           false,
		"192.168.1.10": false,
		"example.com":  false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestValidateListenURL(t *testing.T) {
	cases := []struct {
		listen string
		token  string
		wantOK bool
	}{
		{"http://127.0.0.1:23333", "", true},
		{"http://localhost:23333", "", true},
		{"http://[::1]:23333", "", true},
		{"http://0.0.0.0:23333", "", false}, // non-loopback without token: refuse
		{"http://0.0.0.0:23333", "tok", true},
		{"http://192.168.1.5:23333", "", false},
		{"http://[::]:23333", "", false},
		{"https://127.0.0.1:23333", "", false}, // http scheme only
	}
	for _, c := range cases {
		err := validateListenURL(c.listen, c.token)
		if c.wantOK && err != nil {
			t.Errorf("validateListenURL(%q, %q) unexpected error: %v", c.listen, c.token, err)
		}
		if !c.wantOK && err == nil {
			t.Errorf("validateListenURL(%q, %q) should refuse", c.listen, c.token)
		}
	}
}

func TestGenerateJobID(t *testing.T) {
	if len(generateJobID()) != 32 {
		t.Fatal("job id must be 32 hex chars")
	}
}

func TestSegmentHasPrefix(t *testing.T) {
	if !segmentHasPrefix("/get-tasks/abc", "/get-tasks") {
		t.Error("segment prefix should match")
	}
	if segmentHasPrefix("/get-tasksXYZ", "/get-tasks") {
		t.Error("non-segment prefix should not match")
	}
}

func TestIsSafeCallbackURL(t *testing.T) {
	// Literal-IP branch (upstream semantics).
	if isSafeCallbackURL("http://127.0.0.1:9999/hook") {
		t.Error("loopback literal should be blocked")
	}
	if isSafeCallbackURL("http://localhost/hook") {
		t.Error("localhost should be blocked")
	}
	if isSafeCallbackURL("http://169.254.169.254/hook") {
		t.Error("cloud metadata literal should be blocked")
	}
	if isSafeCallbackURL("http://0.0.0.0:9999/hook") {
		t.Error("unspecified literal should be blocked")
	}
	if !isSafeCallbackURL("http://192.168.1.5/hook") {
		t.Error("RFC1918 LITERAL is allowed (no DNS rebinding possible)")
	}
	if !isSafeCallbackURL("https://example.com/hook") {
		t.Error("public domain should be allowed")
	}
	if isSafeCallbackURL("ftp://example.com/hook") {
		t.Error("non-http scheme should be blocked")
	}
	if !isSafeCallbackURL("") {
		t.Error("empty URL means no webhook configured: legal (upstream)")
	}
}

func TestIsBlockedAddress(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":            true,
		"10.1.2.3":             true,
		"172.16.0.1":           true,
		"172.32.0.1":           false, // outside 172.16/12
		"192.168.1.1":          true,
		"100.64.0.1":           true,
		"100.128.0.1":          false,
		"169.254.1.1":          true,
		"8.8.8.8":              false,
		"fc00::1":              true,
		"2001:4860:4860::8888": false,
	}
	for addr, want := range cases {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("bad test address %q", addr)
		}
		if got := isBlockedAddress(normalizeMappedIP(ip)); got != want {
			t.Errorf("isBlockedAddress(%s) = %v, want %v", addr, got, want)
		}
	}
}
