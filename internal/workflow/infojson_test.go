package workflow

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// --info-json 接线：stdout 必须**只有** JSON（可直接管道解析），且不产生任何产物（只解析不下载）。
//
// 变异验证：去掉 workflow 里那段 InfoJSON 分支 → 输出变成人看的流列表，第一条断言变红。
func TestInfoJSONPrintsMachineReadableMetadata(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cdn, noRewrite := newFakeCDN(t)
	api := newFakeAPI(t, cdn.URL)

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(api.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	// 声明时长要与夹具里的 playurl 时长（10s）一致：差太多会被「试看片段」保护拦下（那条保护本身有独立用例）。
	page := entity.Page{Index: 2, Aid: "1", Cid: "2", Title: "P2 分P", Dur: 10}
	vInfo := &entity.VInfo{Title: "主标题", PagesInfo: []entity.Page{page}}
	cfg := config.DefaultMyOption()
	cfg.InfoJSON = true
	cfg.SkipCover = true
	cfg.RetryDelay = 1
	cfg.ForceReplaceHost, cfg.AllowPcdn = noRewrite()
	out := captureStdout(t, func() {
		wfRun(cfg, client, p, page, vInfo, []entity.Page{page}, download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1})
	})

	// 只取 JSON 部分（日志可能混在前面，这里断言 JSON 可被解析且字段正确）。
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("没有输出 JSON：%s", out)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out[start:]), &got); err != nil {
		t.Fatalf("输出不是合法 JSON：%v\n%s", err, out[start:])
	}
	if got["title"] != "主标题" || got["page_title"] != "P2 分P" {
		t.Errorf("标题字段不符：%v / %v", got["title"], got["page_title"])
	}
	if got["duration_sec"] != float64(10) {
		t.Errorf("duration_sec = %v，期望 10", got["duration_sec"])
	}
	if v, ok := got["video"].([]interface{}); !ok || len(v) == 0 {
		t.Errorf("video 应当非空：%v", got["video"])
	}
	if !strings.Contains(out, "共计1条视频流") == false {
		// 反向断言：--info-json 不该再打印人看的流列表
	}
	if strings.Contains(out, "共计1条视频流") {
		t.Errorf("--info-json 不该打印人看的流列表：%s", out)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("--info-json 不该产出文件，实际有 %s", e.Name())
	}
}
