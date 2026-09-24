package workflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// TestSkipMuxRejectsEmptyRawTrack 钉住 --skip-mux 的产物校验：服务器回 200 + 空体时旧行为会
// 一路判成功，把 0 字节文件当成品交付（混流路径至少还有 ffmpeg 兜底，保留原始轨道时没有）。
//
// 变异验证：删掉 downloadOnePage 里的 verifyArtifact 循环，本用例变红（ok=true、0 字节产物落盘）。
func TestSkipMuxRejectsEmptyRawTrack(t *testing.T) {
	t.Chdir(t.TempDir())

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 + 空体：声明长度 0，旧行为照单全收
	}))
	defer empty.Close()

	playTmpl := "{'code':0,'data':{'dash':{'duration':100,'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
	playURL := strings.ReplaceAll(fmt.Sprintf(playTmpl, empty.URL), "'", string('"'))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(playURL))
	}))
	defer srv.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	cfg := config.DefaultMyOption()
	cfg.AudioOnly = true
	cfg.SkipCover = true
	cfg.SkipSubtitle = true
	cfg.SkipMux = true
	cfg.ForceReplaceHost = false
	cfg.AllowPcdn = true
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
			download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1}, false, nil)
	})
	if ok {
		t.Error("--skip-mux 收到 0 字节轨道时必须判失败（空产物不是有效交付物）")
	}
	if !strings.Contains(out, "音频产物为 0 字节") {
		t.Errorf("缺少可读的产物校验提示，实际输出 %q", out)
	}
	if _, err := os.Stat("out.m4a"); err == nil {
		t.Error("0 字节产物不得落成交付文件")
	}
}
