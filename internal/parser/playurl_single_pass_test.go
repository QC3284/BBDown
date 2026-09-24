package parser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// Playurl 请求策略（本仓优化，见 docs/ROADMAP.md O2）：
// 上游先请求 qn=0、再重发 qn=127，重发带 dash.video 就整份取代前者——常见情况下第一份是白发的。
// 本仓改为 qn=127 优先，仅当它失败或不带 dash.video 时回落 qn=0。
//
// 夹具用的正是上游录制的这一对：dash-reparse-pass1（qn=0）/ dash-reparse-pass2（qn=127）。
//
// 变异验证：把 fetchPlayJSON 的「回落」去掉（直接返回 first/err），case B 变红。

type playurlRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *playurlRecorder) record(u string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, u)
}

func (r *playurlRecorder) count(qn string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.paths {
		if strings.Contains(p, "qn="+qn) {
			n++
		}
	}
	return n
}

// extractWithPlayurlDocs 起一个按 qn 分流的假 API：qn=127 返回 maxDoc，其余返回 lowDoc。
func extractWithPlayurlDocs(t *testing.T, maxDoc, lowDoc string) (*entity.ParsedResult, *playurlRecorder) {
	t.Helper()

	rec := &playurlRecorder{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.String())
		body := lowDoc
		if strings.Contains(r.URL.RawQuery, "qn=127") {
			body = maxDoc
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	p := NewParser(client, cfg)

	result, err := p.ExtractTracks(context.Background(), "1", "1", "2", "", false, false, false, "", false, "")
	if err != nil {
		t.Fatalf("ExtractTracks: %v", err)
	}
	return result, rec
}

func fixtureBody(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return string(b)
}

// A：qn=127 就有 DASH → 只发一次请求，结果取自它。
func TestPlayurlPrefersMaxQnInOneRequest(t *testing.T) {
	maxDoc := fixtureBody(t, "dash-reparse-pass2")
	lowDoc := fixtureBody(t, "dash-reparse-pass1")

	got, rec := extractWithPlayurlDocs(t, maxDoc, lowDoc)
	if n := rec.count("127"); n != 1 {
		t.Errorf("qn=127 请求 %d 次，期望 1 次", n)
	}
	if n := rec.count("0"); n != 0 {
		t.Errorf("qn=0 请求 %d 次，期望 0 次（最高清晰度已可用）", n)
	}
	want, err := extractFixture(t, "dash-reparse-pass2", false)
	if err != nil {
		t.Fatalf("基线解析失败: %v", err)
	}
	if len(got.VideoTracks) != len(want.VideoTracks) || len(got.AudioTracks) != len(want.AudioTracks) {
		t.Errorf("轨道数与 qn=127 文档不一致：video %d/%d, audio %d/%d",
			len(got.VideoTracks), len(want.VideoTracks), len(got.AudioTracks), len(want.AudioTracks))
	}
}

// B：qn=127 不带 DASH → 回落 qn=0，结果取自回落那份（等价于旧流程保留第一份文档）。
func TestPlayurlFallsBackToCallerQn(t *testing.T) {
	noDash := "{'code':0,'data':{'dash':{'duration':1}}}"
	lowDoc := fixtureBody(t, "dash-reparse-pass1")

	got, rec := extractWithPlayurlDocs(t, strings.ReplaceAll(noDash, "'", string('"')), lowDoc)
	if n := rec.count("127"); n != 1 {
		t.Errorf("qn=127 请求 %d 次，期望 1 次", n)
	}
	if n := rec.count("0"); n != 1 {
		t.Errorf("回落 qn=0 请求 %d 次，期望 1 次", n)
	}
	want, err := extractFixture(t, "dash-reparse-pass1", false)
	if err != nil {
		t.Fatalf("基线解析失败: %v", err)
	}
	if len(got.VideoTracks) != len(want.VideoTracks) || len(got.AudioTracks) != len(want.AudioTracks) {
		t.Errorf("回落后的轨道数与 qn=0 文档不一致：video %d/%d, audio %d/%d",
			len(got.VideoTracks), len(want.VideoTracks), len(got.AudioTracks), len(want.AudioTracks))
	}
}
