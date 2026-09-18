package workflow

import (
	"context"
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

// playURLPointsAtDeadHost answers a playurl request whose media URLs point at a
// port nothing listens on, so parsing succeeds and the download fails.
const playURLPointsAtDeadHost = `{"code":0,"data":{"dash":{"duration":100,
  "video":[{"id":80,"codecid":7,"bandwidth":100000,"base_url":"http://127.0.0.1:1/v.m4s"}],
  "audio":[{"id":30280,"codecid":0,"codecs":"mp4a.40.2","bandwidth":64000,"base_url":"http://127.0.0.1:1/a.m4s"}]}}}`

// TestDownloadOnePageRetryDoesNotDeadlock is the regression for the hang where
// Ctrl+C could not stop a running download. Parsing succeeds but the download
// fails, so the page-level retry loop runs a second attempt — and that attempt
// used to take the product lock again. The lock is held until the page returns
// and sync.Mutex is not reentrant, so the goroutine blocked on itself forever.
// A mutex wait ignores context cancellation, which is why even multiple Ctrl+C
// presses had no effect.
func TestDownloadOnePageRetryDoesNotDeadlock(t *testing.T) {
	t.Chdir(t.TempDir()) // the page writes its products under the process directory

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(playURLPointsAtDeadHost))
	}))
	defer srv.Close()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)

	cfg := config.DefaultMyOption()
	cfg.RetryDelay = 20          // ms: keep the retry ladder short for the test
	cfg.ForceReplaceHost = false // do not rewrite the dead-host URLs to a real mirror
	cfg.SkipCover = true
	wf := New(cfg, client)

	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(srv.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 100}
	vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}

	done := make(chan bool, 1)
	go func() {
		done <- wf.downloadOnePage(context.Background(), p, page, vInfo,
			[]entity.Page{page}, "av1", "<videoTitle>", "web",
			map[string]int{}, map[string]int{}, "", "",
			download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1}, false, nil)
	}()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("the page reported success although every download failed")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("downloadOnePage did not return: the retry path deadlocked on the product lock")
	}
}
