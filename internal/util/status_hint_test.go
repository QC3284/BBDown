package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTP412CarriesActionableHint 钉住 412 那条可操作提示（本仓有意加的差异，§4.33）：
// 上游给的是 .NET 默认消息，用户看到 HTTP 412 不知道该等还是该换网络。
//
// 变异验证：撤掉 statusSuffix 的 412 分支，本用例变红。
func TestHTTP412CarriesActionableHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()

	client := NewHTTPClient(func() bool { return false }, func() string { return "" }, nil)
	_, err := client.GetWebSource(context.Background(), srv.URL+"/x/player/wbi/playurl?sign=SECRET")
	if err == nil {
		t.Fatal("HTTP 412 必须报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "412") {
		t.Errorf("错误消息必须带状态码：%q", msg)
	}
	if !strings.Contains(msg, "风控") || !strings.Contains(msg, "更换网络出口") {
		t.Errorf("412 必须给出可操作提示（等一会儿/换出口）：%q", msg)
	}
	if strings.Contains(msg, "SECRET") {
		t.Errorf("URL 必须脱敏：%q", msg)
	}
}

// 其余 4xx 照旧原样抛出：不给 404 套风控提示，否则会误导排查方向。
func TestHTTP404HasNoRiskControlHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewHTTPClient(func() bool { return false }, func() string { return "" }, nil)
	_, err := client.GetWebSource(context.Background(), srv.URL+"/x")
	if err == nil {
		t.Fatal("HTTP 404 必须报错")
	}
	if msg := err.Error(); strings.Contains(msg, "风控") {
		t.Errorf("404 不该带风控提示：%q", msg)
	}
}
