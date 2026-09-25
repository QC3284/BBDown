package workflow

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// --print-urls：只打印所选流的直链后退出，不下载（把本工具当解析器接进脚本/aria2c）。
//
// 变异验证：去掉那段 `if w.Cfg.PrintURLs { ... return true }` → 本用例会看到产物出现，变红。
func TestPrintURLsDoesNotDownload(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	payload := make([]byte, 32<<10)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.m4s", time.Time{}, bytes.NewReader(payload))
	}))
	defer cdn.Close()

	playTmpl := "{'code':0,'data':{'dash':{'duration':10," +
		"'video':[{'id':80,'codecid':7,'codecs':'avc1.640032','bandwidth':1000,'width':640,'height':360,'frame_rate':'30','base_url':'%s/v.m4s'}]," +
		"'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
	body := strings.ReplaceAll(fmt.Sprintf(playTmpl, cdn.URL, cdn.URL), "'", string('"'))
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer api.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(api.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 10}
	vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}
	cfg := config.DefaultMyOption()
	cfg.PrintURLs = true
	cfg.ForceReplaceHost = false // 否则打印的是镜像改写后的地址（测试要断言夹具里的原始地址）
	cfg.AllowPcdn = true         // 假 CDN 的 127.0.0.1 会被 PCDN 正则命中
	cfg.SkipCover = true
	cfg.RetryDelay = 1
	out := captureStdout(t, func() {
		wfRun(cfg, client, p, page, vInfo, []entity.Page{page}, download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1})
	})

	if !strings.Contains(out, cdn.URL+"/v.m4s") || !strings.Contains(out, cdn.URL+"/a.m4s") {
		t.Errorf("应当打印视频与音频直链，实际输出：%s", out)
	}
	if !strings.Contains(out, "--print-urls") && !strings.Contains(out, "共计1条视频流") {
		t.Errorf("输出不像解析结果：%s", out)
	}
	// 关键：不应产生任何产物（只打印，不下载）。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".log") {
			t.Errorf("--print-urls 不该下载，但目录里出现了 %s", filepath.Join(dir, e.Name()))
		}
	}
}
