package workflow

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// TestPageDownloadFallsBackWhenMirrorReplacedHost404s 端到端钉住「替换前原地址」的传递：
// --force-replace-host / --upos-host 把流改写到镜像后，镜像 404 时必须能回到原站把文件下完。
//
// 变异验证：删掉音频下载调用处的 withFallback(...)，本用例变红（下载失败、无产物）。
func TestPageDownloadFallsBackWhenMirrorReplacedHost404s(t *testing.T) {
	t.Chdir(t.TempDir())

	body := make([]byte, 64<<10)
	for i := range body {
		body[i] = byte(i % 241)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer origin.Close()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // 镜像没这个对象
	}))
	defer mirror.Close()

	playTmpl := "{'code':0,'data':{'dash':{'duration':100,'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
	playURL := strings.ReplaceAll(fmt.Sprintf(playTmpl, origin.URL), "'", string('"'))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(playURL))
	}))
	defer srv.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	cfg := config.DefaultMyOption()
	cfg.AudioOnly = true
	cfg.SkipCover = true
	cfg.SkipMux = true
	cfg.ForceReplaceHost = false
	cfg.UposHost = strings.TrimPrefix(mirror.URL, "http://") // 替换成恒 404 的假镜像
	cfg.RetryCount = 2
	cfg.RetryDelay = 1
	wf := New(cfg, client)

	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(srv.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 100}
	vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}

	var ok bool
	out := captureStdout(t, func() {
		ok = wf.downloadOnePage(context.Background(), p, page, vInfo,
			[]entity.Page{page}, "av1", "out.m4a", "web",
			map[string]int{}, map[string]int{}, "", "",
			download.DownloadConfig{Client: client, RetryCount: 2, RetryDelayMs: 1}, false, nil)
	})
	if !ok {
		t.Fatalf("镜像 404 时应回退原站并成功，输出: %s", out)
	}
	// --audio-only + --skip-mux：下载完的音频会被改名为最终产物 savePath（此处由用例指定）。
	got, err := os.ReadFile("out.m4a")
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("回退后的产物不符：err=%v 大小=%d 期望=%d", err, len(got), len(body))
	}
}
