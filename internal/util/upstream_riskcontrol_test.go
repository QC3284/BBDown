package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 上游 RiskControlResponseException 的等价物：接口以 HTTP 200 返回 HTML（风控页/登录墙）时，
// 必须给出可读诊断，而不是让下游 JSON 解析报 invalid character '<'。

func TestUpstreamRiskControlPageDetection(t *testing.T) {
	// 前导空白与 UTF-8 BOM 都要剥掉（部分 WAF/风控页在 '<html' 前带换行）
	for _, html := range []string{"<html>", "  \n<html>", "\uFEFF<html>", "\n\uFEFF  <!DOCTYPE html>"} {
		if !LooksLikeHTMLPage(html) {
			t.Errorf("%q 应被判为 HTML 页面", html)
		}
	}
	for _, js := range []string{"", `{"code":0}`, "[]", "   \n{}"} {
		if LooksLikeHTMLPage(js) {
			t.Errorf("%q 不应被判为 HTML 页面", js)
		}
	}

	var v map[string]interface{}
	err := UnmarshalJSON("<html><body>风控</body></html>", &v)
	if err == nil {
		t.Fatal("HTML 响应必须报错")
	}
	if !strings.Contains(err.Error(), "风控") {
		t.Errorf("诊断信息应提到风控页，实际 %q", err)
	}
	if strings.Contains(err.Error(), "invalid character") {
		t.Errorf("不应把裸 JSON 语法错误抛给用户: %q", err)
	}

	if err := UnmarshalJSON(`{"code":0}`, &v); err != nil {
		t.Errorf("正常 JSON 不应报错: %v", err)
	}
	if v["code"] != float64(0) {
		t.Errorf("解析结果不对: %v", v)
	}
}

// TestRiskControlPageSurfacesThroughFetchPath 走一遍真实链路：假服务器以 200 + HTML
// 回应「API」请求，取回响应体后按各调用点的方式做 JSON 解析。

func TestRiskControlPageSurfacesThroughFetchPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><head><title>风控校验</title></head></html>"))
	}))
	defer srv.Close()

	c := NewHTTPClient(nil, func() string { return "" }, nil)
	body, err := c.GetWebSource(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("取回响应体失败: %v", err)
	}

	var root map[string]interface{}
	err = UnmarshalJSON(body, &root)
	if err == nil {
		t.Fatal("风控 HTML 页面必须在 JSON 边界被识别")
	}
	if !strings.Contains(err.Error(), "风控") {
		t.Errorf("诊断信息应提到风控页，实际 %q", err)
	}
}
