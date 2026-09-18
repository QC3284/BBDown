package workflow

import (
	"context"
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

// audioOnlyPlayURL 只返回音频流的 playurl 响应：用来验证 --video-only 的零产物判定。
const audioOnlyPlayURL = `{"code":0,"data":{"dash":{"duration":100,
  "audio":[{"id":30280,"codecid":0,"codecs":"mp4a.40.2","bandwidth":64000,"base_url":"http://127.0.0.1:1/a.m4s"}]}}}`

// TestVideoOnlyWithoutVideoTracksFails 对齐上游 Download.cs:553-560：dash 分支没有视频流时
// 要打印「没有找到符合要求的视频流」，且 --video-only 必须判失败——
// 此前只清空轨道不报错，用户拿到零产物却看到「任务完成」。
func TestVideoOnlyWithoutVideoTracksFails(t *testing.T) {
	t.Chdir(t.TempDir())

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(audioOnlyPlayURL))
	}))
	defer srv.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	cfg := config.DefaultMyOption()
	cfg.VideoOnly = true
	cfg.SkipCover = true
	cfg.ForceReplaceHost = false
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
			download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1}, false, nil)
	})
	if ok {
		t.Error("--video-only 遇到没有视频流的稿件必须判失败（零产物不能报成功）")
	}
	if !strings.Contains(out, "没有找到符合要求的视频流") {
		t.Errorf("缺少提示，实际输出 %q", out)
	}
}
