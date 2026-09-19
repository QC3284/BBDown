package workflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// TestRetryLadderAttemptsMatchUpstream 钉住两级重试的总请求次数。
//
// 上游对同一个恒失败的轨道就是「页面级 × 轨道级」：
//   - 页面级 DownloadPageAsync：while (retryCount < maxRetry)，maxRetry = --retry-count（默认 3）；
//   - 轨道级 DownloadFileCoreAsync：while (retry < maxRetry)，maxRetry = Config.Current.MaxRetryCount，
//     同样来自 --retry-count（Workflow.cs 里 MaxRetryCount: myOption.RetryCount）。
//
// 所以一个恒 404 的音轨会被 GET 3×3 = 9 次，另有每次 DownloadFile 的 1 次 HEAD 探测。
// 用户问「重试貌似进行了 3x3=9 次」——9 就是上游行为，不是本仓多试了。
func TestRetryLadderAttemptsMatchUpstream(t *testing.T) {
	t.Chdir(t.TempDir())

	var heads, gets atomic.Int64
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads.Add(1)
		} else {
			gets.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer cdn.Close()

	// JSON 用单引号写、再整体换成双引号：用例里没有一处转义，读起来就是原始 JSON。
	playTmpl := "{'code':0,'data':{'dash':{'duration':100,'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
	playURL := strings.ReplaceAll(fmt.Sprintf(playTmpl, cdn.URL), "'", string('"'))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(playURL))
	}))
	defer srv.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	cfg := config.DefaultMyOption()
	cfg.AudioOnly = true
	cfg.SkipCover = true
	cfg.ForceReplaceHost = false
	cfg.AllowPcdn = true // 否则 PCDN 镜像替换会把请求从假服务器上引开
	cfg.RetryCount = 3
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
			[]entity.Page{page}, "av1", "<videoTitle>", "web",
			map[string]int{}, map[string]int{}, "", "",
			download.DownloadConfig{Client: client, RetryCount: 3, RetryDelayMs: 1}, false, nil)
	})
	if ok {
		t.Fatal("下载全失败时必须判失败（零产物不能报成功）")
	}
	t.Logf("GET=%d HEAD=%d", gets.Load(), heads.Load())
	if got := gets.Load(); got != 9 {
		t.Errorf("轨道 GET 次数 = %d，期望 9（上游页面级 3 × 轨道级 3）", got)
	}
	// 两级重试必须能分辨：轨道级（上游记 Debug）不该在默认级别刷屏，页面级只在
	// 「还有下一次机会」时才打 Warn（第 3 次直接判失败）——所以默认级别下整段输出
	// 只该有 2 条重试日志。轨道级一旦退回 Warn，这里会是 8 条，用户读起来就是 9 连试。
	if got := strings.Count(out, "重试"); got != 2 {
		t.Errorf("默认级别下重试日志 %d 条，期望 2 条（页面级那两次）：轨道级若不记 Debug 就会像 9 连试", got)
	}
}
