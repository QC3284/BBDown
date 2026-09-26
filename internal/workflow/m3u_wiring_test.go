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

// TestWriteM3UWiringAtProductAndSkipPaths 端到端钉住 --write-m3u 的两处接线：
// 真下载走「产物成功点」，产物已存在走「已存在, 跳过下载」提前返回。
// 只测渲染/写入函数是不够的——把调用删掉，纯函数用例照样全绿（ROADMAP 记过同类坑）。
//
// 变异验证：
//   - 删掉 downloadOnePage 产物成功点的 w.writeM3USidecar(...) → 第一段红（没有 t.m3u）；
//   - 删掉「已存在, 跳过下载」分支的 w.writeM3USidecar(...) → 第二段红（删掉列表后重跑仍不生成）。
func TestWriteM3UWiringAtProductAndSkipPaths(t *testing.T) {
	t.Chdir(t.TempDir())

	body := make([]byte, 32<<10)
	for i := range body {
		body[i] = byte(i % 241)
	}
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4s", time.Time{}, bytes.NewReader(body))
	}))
	defer cdn.Close()

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
	cfg.SkipSubtitle = true
	cfg.SkipMux = true
	cfg.ForceReplaceHost = false
	cfg.AllowPcdn = true
	cfg.RetryDelay = 1
	cfg.WriteM3U = true
	wf := New(cfg, client)

	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(srv.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 100}
	vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}

	run := func() (bool, string) {
		var ok bool
		out := captureStdout(t, func() {
			ok = wf.downloadOnePage(context.Background(), p, page, vInfo,
				[]entity.Page{page}, "av1", "out.m4a", "web",
				map[string]int{}, map[string]int{}, "", "",
				download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1}, false, nil)
		})
		return ok, out
	}

	// 第一段：真下载 → 产物成功点登记。
	ok, out := run()
	if !ok {
		t.Fatalf("首次下载应当成功，输出: %s", out)
	}
	if _, err := os.Stat("out.m4a"); err != nil {
		t.Fatalf("首次下载应当产出 out.m4a：%v", err)
	}
	assertPlaylist(t, "t.m3u", "#EXTM3U\n#EXTINF:-1,t\nout.m4a\n")

	// 第二段：产物已存在（走跳过分支，不重新下载）——先把列表删掉，证明「已产出」的产物
	// 在补开/重跑时仍会被登记，而不是只有本次新下载的分P才进列表。
	if err := os.Remove("t.m3u"); err != nil {
		t.Fatal(err)
	}
	ok, out = run()
	if !ok {
		t.Fatalf("产物已存在时应判成功（跳过下载），输出: %s", out)
	}
	if !strings.Contains(out, "已存在, 跳过下载") {
		t.Fatalf("第二段应当走「已存在, 跳过下载」分支，实际输出: %s", out)
	}
	assertPlaylist(t, "t.m3u", "#EXTM3U\n#EXTINF:-1,t\nout.m4a\n")
}

func assertPlaylist(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("应当写出播放列表 %s：%v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
