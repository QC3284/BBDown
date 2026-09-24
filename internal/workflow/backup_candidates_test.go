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

// TestPageDownloadUsesPlayurlBackupURLCandidates 钉住 F4 的端到端接线：候选链是数据驱动的
// （playurl 的 backup_url → entity.BackupURLs → DownloadConfig.FallbackURLs），没有新增开关。
//
// 场景：--upos-host 指到一台恒 404 的镜像（主地址），playurl 的 base_url 也连不上，只有
// backup_url 可用。链上有 3 个候选而 --retry-count 只有 2——每个候选至少出场一次，第三个
// 候选必须被走到。
//
// 变异验证：withFallback 少传 selectedAudio.BackupURLs（或解析层不再收 backup_url），
// 本用例变红（下载失败、无产物）。
func TestPageDownloadUsesPlayurlBackupURLCandidates(t *testing.T) {
	t.Chdir(t.TempDir())

	body := make([]byte, 32<<10)
	for i := range body {
		body[i] = byte(i % 241)
	}
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer backup.Close()

	deadMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer deadMirror.Close()

	// playurl 的 base_url 指向一个无人监听的端口（连接被拒），backup_url 才是活地址。
	playTmpl := "{'code':0,'data':{'dash':{'duration':100,'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'http://127.0.0.1:1/a.m4s','backup_url':['%s/a.m4s']}]}}}"
	playURL := strings.ReplaceAll(fmt.Sprintf(playTmpl, backup.URL), "'", string('"'))
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
	cfg.UposHost = strings.TrimPrefix(deadMirror.URL, "http://") // 主地址被替换成恒 404 的假镜像
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
		t.Fatalf("镜像与 base_url 都不可用时，应沿候选链走到 backup_url 并成功，输出: %s", out)
	}
	got, err := os.ReadFile("out.m4a")
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("产物不符：err=%v 大小=%d 期望=%d", err, len(got), len(body))
	}
}
