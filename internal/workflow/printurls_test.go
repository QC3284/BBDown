package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// 假 CDN + 假 API 用公共辅助：它会一并给出「关掉镜像替换 / 允许 PCDN」的配置，
	// 免得每个用例都手写一遍，并再次踩「假地址被生产逻辑改写」的坑（见 fakecdn_test.go）。
	cdn, noRewrite := newFakeCDN(t)
	api := newFakeAPI(t, cdn.URL)

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
	cfg.ForceReplaceHost, cfg.AllowPcdn = noRewrite()
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
