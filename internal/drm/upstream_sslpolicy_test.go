package drm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// 本文件搬上游 VerifiedNoRedirectClientTests / HttpUtilSslPolicyTests 的可移植部分：
// 许可证请求必须 ① 不跟随重定向（请求体是设备私钥签名的 challenge，307/308 连同 body
// 重放等于把签名送出去）② 始终校验 TLS（不随 --insecure 降级）。

func TestUpstreamLicenseClientDoesNotFollowRedirect(t *testing.T) {
	var targetHits, redirects int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			atomic.AddInt32(&targetHits, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt32(&redirects, 1)
		http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	orig := licenseURL
	licenseURL = srv.URL + "/start"
	t.Cleanup(func() { licenseURL = orig })

	cdm := &WidevineCdm{}
	_, err := cdm.sendRequest(context.Background(), []byte("challenge"))
	if err == nil {
		t.Fatal("307 应作为响应返回而不是被跟随")
	}
	if !strings.Contains(err.Error(), "307") {
		t.Errorf("错误信息应体现 307 未被跟随，实际 %q", err)
	}
	if got := atomic.LoadInt32(&targetHits); got != 0 {
		t.Errorf("重定向目标被请求了 %d 次——签名 body 已外发", got)
	}
	if got := atomic.LoadInt32(&redirects); got != 1 {
		t.Errorf("起始请求次数 = %d，期望 1", got)
	}
}

func TestUpstreamLicenseClientAlwaysVerifiesTLS(t *testing.T) {
	// httptest.NewTLSServer 用的是自签证书：验证生效时握手必然失败
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	orig := licenseURL
	licenseURL = srv.URL
	t.Cleanup(func() { licenseURL = orig })

	cdm := &WidevineCdm{}
	_, err := cdm.sendRequest(context.Background(), []byte("challenge"))
	if err == nil {
		t.Fatal("自签证书必须导致失败——许可证链路不能随 --insecure 降级")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("应为证书校验错误，实际 %q", err)
	}
}

// TestInsecureClientTogglesTLSVerification 对应上游 HttpUtilSslPolicyTests：
// skipSSL 为真时跳过校验、为假时校验生效（本仓由构造期决定，每个 client 一套连接池）。

func TestInsecureClientTogglesTLSVerification(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	insecure := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	if _, err := insecure.GetWebSource(ctx, srv.URL); err != nil {
		t.Errorf("--insecure 应跳过证书校验: %v", err)
	}

	verified := util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil)
	if _, err := verified.GetWebSource(ctx, srv.URL); err == nil {
		t.Error("默认必须校验证书，自签站点应失败")
	}
}
