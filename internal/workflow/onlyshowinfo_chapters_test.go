package workflow

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// -I（--only-show-info）只打印流信息，不产出混流产物，因此没理由去抓章节（player/wbi/v2）。
// 这是本仓优化（docs/ROADMAP.md O2）：单次 -I 解析原本 5 个 GET，其中一个是白发的章节请求。
//
// fetchPoints 的 URL 硬编码 api.bilibili.com（与上游一致），假服务器拦不下它，所以这里用接缝
// （fetchPointsFunc）计数——既能离线断言，也刚好钉住「只在非 -I 时才调用」这个门槛。
//
// 变异验证：把调用处的 `if !w.Cfg.OnlyShowInfo` 去掉，本用例变红。
func TestOnlyShowInfoSkipsChapterFetch(t *testing.T) {
	t.Chdir(t.TempDir())

	var calls atomic.Int64
	orig := fetchPointsFunc
	fetchPointsFunc = func(context.Context, *util.HTTPClient, string, string) []entity.ViewPoint {
		calls.Add(1)
		return nil
	}
	t.Cleanup(func() { fetchPointsFunc = orig })

	body := make([]byte, 32<<10)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer cdn.Close()

	playTmpl := "{'code':0,'data':{'dash':{'duration':100," +
		"'video':[{'id':80,'codecid':7,'codecs':'avc1.640032','bandwidth':2000000,'width':1920,'height':1080,'frame_rate':'30','base_url':'%s/v.m4s'}]," +
		"'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
	playURL := strings.ReplaceAll(fmt.Sprintf(playTmpl, cdn.URL, cdn.URL), "'", string('"'))
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(playURL))
	}))
	defer api.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(api.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 100}
	vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}
	pages := []entity.Page{page}

	run := func(cfg config.MyOption) {
		captureStdout(t, func() {
			wfRun(cfg, client, p, page, vInfo, pages, download.DownloadConfig{Client: client, RetryCount: 2, RetryDelayMs: 1})
		})
	}

	// -I：只打印流，章节用不到 → 不该调用。
	cfgShow := config.DefaultMyOption()
	cfgShow.OnlyShowInfo = true
	cfgShow.SkipCover = true
	run(cfgShow)
	if n := calls.Load(); n != 0 {
		t.Errorf("-I 模式抓了 %d 次章节，期望 0 次", n)
	}

	// 非 -I：章节要写进产物元数据 → 必须照旧抓一次（否则上面的断言对「无条件跳过」也成立）。
	calls.Store(0)
	cfgFull := config.DefaultMyOption()
	cfgFull.SkipCover = true
	cfgFull.SkipSubtitle = true
	cfgFull.SkipMux = true // 不需要 ffmpeg：保留原始轨道即可
	cfgFull.RetryDelay = 1 // 这个夹具的页面会失败并走满页面级重试，把退避压到 1ms 让用例快
	run(cfgFull)
	// 页面失败时会走满页面级重试，每轮都重新抓章节（与上游一致），所以这里只要求「抓过」。
	if n := calls.Load(); n == 0 {
		t.Error("非 -I 模式一次章节都没抓：门槛写反了（章节要写进产物元数据）")
	}
}

// wfRun 把 downloadOnePage 的固定参数收在一处，避免两段用例漂移。
func wfRun(cfg config.MyOption, client *util.HTTPClient, p *parser.Parser, page entity.Page,
	vInfo *entity.VInfo, pages []entity.Page, dlCfg download.DownloadConfig) bool {
	wf := New(cfg, client)
	return wf.downloadOnePage(context.Background(), p, page, vInfo, pages, "av1", "out.m4a", "web",
		map[string]int{}, map[string]int{}, "", "", dlCfg, false, nil)
}
