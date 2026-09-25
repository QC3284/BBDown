package workflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// --sub-only 但一个字幕都没拿到时必须给提示：真机实测里它 exit 0、打印「任务完成」却没有任何产物，
// 用户分不清「没字幕」「没登录」「下载失败」。
//
// 变异验证：去掉那段 LogWarn → 本用例变红。
func TestSubOnlyWarnsWhenNoSubtitleFound(t *testing.T) {
	t.Chdir(t.TempDir())

	// 假 API：所有请求都回 playurl（字幕接口必然解析不出字幕）。
	body := strings.ReplaceAll("{'code':0,'data':{'dash':{'duration':10,'video':[{'id':80,'codecid':7,'codecs':'avc1.640032','bandwidth':1000,'width':640,'height':360,'frame_rate':'30','base_url':'https://cdn.example.com/v.m4s'}],'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'https://cdn.example.com/a.m4s'}]}}}", "'", string('"'))
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
	cfg.SubOnly = true
	cfg.SkipCover = true
	cfg.RetryDelay = 1
	out := captureStdout(t, func() {
		wfRun(cfg, client, p, page, vInfo, []entity.Page{page}, download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1})
	})
	if !strings.Contains(out, "未找到可用字幕") {
		t.Errorf("--sub-only 没有字幕时必须提示，实际输出：%s", out)
	}
}
