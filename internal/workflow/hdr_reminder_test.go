package workflow

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// HDR Vivid(129) 兼容提醒：档位最高不等于最稳，不支持的设备上会偏色/无法播放。
// 提醒在**选流之后**（`-I` 在选流前就返回了，所以只走下载路径也要覆盖得到）。
//
// 变异验证：去掉 workflow.go 里那段 LogWarn → 第一条断言变红。
func TestHDRVividReminderShownOnlyForHDRTracks(t *testing.T) {
	t.Chdir(t.TempDir())

	payload := make([]byte, 64<<10)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.m4s", time.Time{}, bytes.NewReader(payload))
	}))
	defer cdn.Close()

	runWith := func(videoID string) string {
		playTmpl := "{'code':0,'data':{'dash':{'duration':100," +
			"'video':[{'id':%s,'codecid':7,'codecs':'avc1.640032','bandwidth':2000000,'width':1920,'height':1080,'frame_rate':'30','base_url':'%s/v.m4s'}]," +
			"'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':64000,'base_url':'%s/a.m4s'}]}}}"
		body := strings.ReplaceAll(fmt.Sprintf(playTmpl, videoID, cdn.URL, cdn.URL), "'", string('"'))
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

		page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 100}
		vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}
		cfg := config.DefaultMyOption()
		cfg.SkipMux = true // 不需要 ffmpeg：保留原始轨道即可
		cfg.SkipCover = true
		cfg.SkipSubtitle = true
		cfg.RetryDelay = 1
		return captureStdout(t, func() {
			wfRun(cfg, client, p, page, vInfo, []entity.Page{page}, download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1})
		})
	}

	// 断言用提醒特有的短语「偏色」，避免与流列表里的 dfn 标签（HDR Vivid）混淆。
	if out := runWith("129"); !strings.Contains(out, "偏色") {
		t.Errorf("选中 HDR Vivid 时必须给出兼容提醒，实际输出：%s", out)
	}
	if out := runWith("80"); strings.Contains(out, "偏色") {
		t.Errorf("普通档位不该出现 HDR 提醒，实际输出：%s", out)
	}
}
