package workflow

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newFakeCDN 起一个本地假 CDN 并返回它的地址。
//
// 为什么需要它（三次踩坑的固化）：用例里的假地址会被**生产逻辑**改写——
//  1. `--force-replace-host`/`--upos-host` 的镜像替换会把 host 换掉；
//  2. PCDN 正则把 `127.0.0.1` 这种 IP 直连当成 PCDN 而改写域名。
//
// 写法上「记得关掉这两处」靠人是靠不住的（本仓真栽过两次），所以由辅助函数返回相应的配置项，
// 调用方直接赋值即可：
//
//	cdn, noRewrite := newFakeCDN(t)
//	cfg.ForceReplaceHost, cfg.AllowPcdn = noRewrite()
func newFakeCDN(t *testing.T) (*httptest.Server, func() (bool, bool)) {
	t.Helper()
	payload := make([]byte, 32<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.m4s", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(srv.Close)
	return srv, func() (bool, bool) { return false, true } // ForceReplaceHost=false, AllowPcdn=true
}

// newFakeAPI 起一个假 API：所有请求都回同一个 playurl 文档（视频/音频指向 cdnURL）。
// 用法与 newFakeCDN 配对，避免各处再手写 playurl 模板与 TLS 客户端。
func newFakeAPI(t *testing.T, cdnURL string) *httptest.Server {
	t.Helper()
	playTmpl := "{'code':0,'data':{'dash':{'duration':10," +
		"'video':[{'id':80,'codecid':7,'codecs':'avc1.640032','bandwidth':1000,'width':640,'height':360,'frame_rate':'30','base_url':'%s/v.m4s'}]," +
		"'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
	body := strings.ReplaceAll(strings.ReplaceAll(playTmpl, "'", string('"')), "%s", cdnURL)
	// 上面替换了全部 %s（两个轨道），因此直接复用 cdnURL 两次的语义。
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.Close)
	return api
}
