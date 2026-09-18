package util

import "testing"

func TestNormalizeCredentialHost(t *testing.T) {
	cases := map[string]string{
		"api.bilibili.com":            "api.bilibili.com",
		"https://api.bilibili.com":    "api.bilibili.com",
		"http://127.0.0.1:8080":       "127.0.0.1",
		"https://Mirror.Example/Path": "mirror.example",
		"":                            "",
	}
	for in, want := range cases {
		if got := normalizeCredentialHost(in); got != want {
			t.Errorf("normalizeCredentialHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMaySendCredentials: cookies belong to the official domains and to hosts the
// user explicitly configured; anything else must be refused so a URL built from
// server-controlled data cannot exfiltrate SESSDATA.
func TestMaySendCredentials(t *testing.T) {
	c := NewHTTPClient(nil, func() string { return "SESSDATA=secret" }, nil)

	for _, u := range []string{
		"https://api.bilibili.com/x/player/playurl",
		"https://passport.bilibili.com/qrcode",
		"https://upos-sz-mirrorcoso1.bilivideo.com/x.m4s",
	} {
		if !c.maySendCredentials(u) {
			t.Errorf("%s must be allowed", u)
		}
	}
	for _, u := range []string{
		"https://evil.example/steal",
		"http://127.0.0.1:9999/x",
		"https://bilibili.com.evil.example/x",
		"not a url at all",
	} {
		if c.maySendCredentials(u) {
			t.Errorf("%s must be refused", u)
		}
	}

	// An explicitly configured mirror is allowed.
	c.SetCredentialHosts("mirror.example", "http://127.0.0.1:8080", "")
	if !c.maySendCredentials("https://mirror.example/pgc/player/web/v2/playurl") {
		t.Error("a configured --host must be allowed to receive cookies")
	}
	if !c.maySendCredentials("http://127.0.0.1:8080/x") {
		t.Error("a configured host with a port must be allowed")
	}
	if c.maySendCredentials("https://other.example/x") {
		t.Error("an unconfigured host must stay refused")
	}
}
