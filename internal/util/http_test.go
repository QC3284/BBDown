package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIsTrustedCookieHost(t *testing.T) {
	trusted := []string{"bilibili.com", "api.bilibili.com", "passport.bilibili.com", "b23.tv", "upos-sz-mirrorcoso1.bilivideo.com", "i0.hdslb.com"}
	for _, h := range trusted {
		if !IsTrustedCookieHost(h) {
			t.Errorf("IsTrustedCookieHost(%q) = false, want true", h)
		}
	}
	untrusted := []string{"", "127.0.0.1", "evil.example", "bilibili.com.evil.example", "notbilibili.com", "evilibili.com"}
	for _, h := range untrusted {
		if IsTrustedCookieHost(h) {
			t.Errorf("IsTrustedCookieHost(%q) = true, want false", h)
		}
	}
}

// TestCredentialRedirectGuardBlocksExfiltration is the regression for the
// missing redirect guard: a credential-bearing GET followed the 3xx and handed
// SESSDATA to whatever host the response pointed at.
func TestCredentialRedirectGuardBlocksExfiltration(t *testing.T) {
	var leaked atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte("evil"))
	}))
	defer evil.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}))
	defer redirector.Close()

	c := NewHTTPClient(nil, func() string { return "SESSDATA=secret" }, nil)
	if _, err := c.GetWebSource(context.Background(), redirector.URL); err == nil {
		t.Fatal("expected the credential-bearing request to refuse the cross-host redirect")
	}
	if leaked.Load() {
		t.Fatal("credentials were forwarded to an untrusted host")
	}
}

// TestPostFormRefusesCrossHostRedirect covers the same guard on the POST path
// (gRPC / login polling share this client).
func TestPostFormRefusesCrossHostRedirect(t *testing.T) {
	var reached atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		_, _ = w.Write([]byte("evil"))
	}))
	defer evil.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	c := NewHTTPClient(nil, nil, nil)
	if _, err := c.PostForm(context.Background(), redirector.URL, nil); err == nil {
		t.Fatal("expected the POST to refuse the cross-host redirect")
	}
	if reached.Load() {
		t.Fatal("a cross-host redirect from a POST was followed")
	}
}
