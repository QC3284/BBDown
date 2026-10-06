package util

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestDebugLineClampsScreenKeepsFullInFile 钉住 t42 的 --debug 宽度治理：
// 屏幕上的诊断行按显示宽度夹取（t39 实测 Response 最长 716 列、GET 248 列），
// 而**日志文件里仍是全文**（排查靠它）。
//
// 变异验证：撤掉 debugLine 里的 screenClamp（直接交给 debugFn）→ 屏幕行 600+ 列 → 本用例红。
func TestDebugLineClampsScreenKeepsFullInFile(t *testing.T) {
	// 长响应：300 个汉字 ≈ 600 显示列——屏幕必须夹，文件必须全。
	body := "中文响应" + strings.Repeat("汉", 300) + "结尾"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var screen []string
	c := NewHTTPClient(func() bool { return true }, func() string { return "" },
		func(format string, args ...interface{}) { screen = append(screen, fmt.Sprintf(format, args...)) })

	logPath := filepath.Join(t.TempDir(), "bbdown.log")
	SetLogFile(logPath)
	defer SetLogFile("")

	if _, err := c.GetWebSource(context.Background(), srv.URL+"/x"); err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	if len(screen) < 2 {
		t.Fatalf("期望 GET 与 Response 两条诊断行，实际 %q", screen)
	}
	var sawResponse bool
	for _, line := range screen {
		// 屏幕实际那一行 = 28 列时间戳前缀 + 正文（前缀由 util.LogDebug 打）。
		if w := widthOf(t, line) + LogIndentWidth; w > debugScreenMaxCols {
			t.Errorf("屏幕诊断行（含时间戳）%d 列 > %d：%q", w, debugScreenMaxCols, line)
		}
		if strings.HasPrefix(line, "Response: ") {
			sawResponse = true
			if !strings.HasSuffix(line, "…") {
				t.Errorf("被夹取的屏幕行应当以「…」收尾：%q", line)
			}
		}
	}
	if !sawResponse {
		t.Fatalf("没有看到 Response 诊断行：%q", screen)
	}

	// 日志文件：必须含**全文**（屏幕夹掉的那部分也在）+ 完整的 GET 行。
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读日志文件失败: %v", err)
	}
	if !strings.Contains(string(logged), body) {
		t.Errorf("日志文件应当含完整响应体（%d 字节），实际日志 %d 字节", len(body), len(logged))
	}
	if !strings.Contains(string(logged), "GET "+srv.URL) {
		t.Errorf("日志文件里应当有完整的 GET 行：%s", logged)
	}
}

// TestScreenClamp 钉住夹取本身：按**显示宽度**（CJK 2 列）而不是字节数/字符数。
func TestScreenClamp(t *testing.T) {
	cases := []struct {
		name string
		in   string
		cols int
		want string
	}{
		{"放得下就原样", "abc", 10, "abc"},
		{"超出即夹取并加省略号", "abcdefghij", 5, "abcd…"},
		{"中文按 2 列算（5 列只放得下 2 个汉字）", "汉汉汉", 5, "汉汉…"},
		{"交通类 emoji 按 2 列算（🚀 在 1F680-1F6FF）", "a🚀", 3, "a🚀"},
		{"交通类 emoji 超宽被夹取（🚀 占 2 列）", "a🚀b", 3, "a…"},
		{"列数为 1 只剩省略号", "汉汉", 1, "…"},
		{"列数 0 返回空串", "abc", 0, ""},
		{"空串", "", 10, ""},
	}
	for _, c := range cases {
		if got := screenClamp(c.in, c.cols); got != c.want {
			t.Errorf("%s：screenClamp(%q, %d) = %q, want %q", c.name, c.in, c.cols, got, c.want)
		}
	}
}

// widthOf 用 screenClamp 自身的口径反推显示宽度：最小的、不触发夹取的列数就是宽度
// （unclamped 当且仅当 width ≤ cols）。这样用例不必再养一份会走样的宽度表。
func widthOf(t *testing.T, s string) int {
	t.Helper()
	for w := 0; w <= len(s); w++ {
		if screenClamp(s, w) == s {
			return w
		}
	}
	t.Fatalf("量不出宽度：%q", s)
	return 0
}
