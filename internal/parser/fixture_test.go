package parser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// Fixture replay harness, ported from upstream ParserFixtureTests: the fixtures
// in testdata/ are the real API responses upstream recorded, and config.Host
// points the parser at a local TLS server that replays them. This locks the
// parse trunk (data root, track mapping, DRM extraction, error propagation)
// without touching the network, so a future API shape change turns a test red
// instead of silently changing behaviour.
func extractFixture(t *testing.T, fixture string, wantDrm bool) (*entity.ParsedResult, error) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", fixture+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", fixture, err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	cfg.Cookie = ""
	cfg.Token = ""

	// skipSSL is true so the client trusts the test server certificate.
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	return NewParser(client, cfg).ExtractTracks(context.Background(), "av170001", "170001", "999", "", false, false, false, "", wantDrm, "0")
}

// TestFixtureDrmDashBadKidProducesNoKeyId: a malformed bilidrm_uri carrying a
// host, path and query must not become a key id — taking everything after "//"
// used to store "evil.example/path?x=1" as the kid.
func TestFixtureDrmDashBadKidProducesNoKeyId(t *testing.T) {
	result, err := extractFixture(t, "drm-dash-badkid", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsDrm {
		t.Error("IsDrm = false, want true")
	}
	if result.KidHex != "" {
		t.Errorf("KidHex = %q, want empty for a malformed bilidrm_uri", result.KidHex)
	}
	if result.PsshBase64 != "" {
		t.Errorf("PsshBase64 = %q, want empty", result.PsshBase64)
	}
}

// TestFixtureDrmDashExtractsKeyId is the positive control for the case above.
func TestFixtureDrmDashExtractsKeyId(t *testing.T) {
	result, err := extractFixture(t, "drm-dash", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsDrm {
		t.Error("IsDrm = false, want true")
	}
	if result.KidHex != "0123456789abcdef0123456789abcdef" {
		t.Errorf("KidHex = %q, want the 32-hex key id", result.KidHex)
	}
}

// TestFixtureBizErrorIsReported: a business-level error response must surface as
// an error, not as an empty success.
func TestFixtureBizErrorIsReported(t *testing.T) {
	if _, err := extractFixture(t, "biz-error", false); err == nil {
		t.Error("a code != 0 response produced no error")
	}
}

// TestFixtureMissingNodesTolerated: optional nodes absent from the response must
// not panic. Reaching the end of this test without the framework recovering a
// panic is the assertion.
func TestFixtureMissingNodesTolerated(t *testing.T) {
	_, _ = extractFixture(t, "missing-nodes-tolerant", false)
}

// TestFixtureDolbyAndFlacAudioAppended: dash.dolby.audio[] and dash.flac.audio
// must be appended to the audio track list, not dropped (upstream F11).
func TestFixtureDolbyAndFlacAudioAppended(t *testing.T) {
	result, err := extractFixture(t, "dolby-flac-audio", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.VideoTracks) != 1 {
		t.Errorf("video tracks = %d, want 1", len(result.VideoTracks))
	}
	if len(result.AudioTracks) != 4 {
		t.Errorf("audio tracks = %d, want 4 (1 normal + 2 dolby + 1 flac)", len(result.AudioTracks))
	}
}

// extractFixturePasses drives a parse against a server that answers the qn=0
// pass with first and the qn=127 re-request with second, recording the qn of
// every request.
func extractFixturePasses(t *testing.T, first, second string) (*entity.ParsedResult, []string, error) {
	t.Helper()

	load := func(name string) []byte {
		body, err := os.ReadFile(filepath.Join("testdata", name+".json"))
		if err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
		return body
	}
	firstBody, secondBody := load(first), load(second)

	var qns []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qn := r.URL.Query().Get("qn")
		qns = append(qns, qn)
		w.Header().Set("Content-Type", "application/json")
		if qn == maxQn {
			_, _ = w.Write(secondBody)
			return
		}
		_, _ = w.Write(firstBody)
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	cfg.Cookie = ""
	cfg.Token = ""

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	res, err := NewParser(client, cfg).ExtractTracks(context.Background(), "av170001", "170001", "999", "", false, false, false, "", false, "0")
	return res, qns, err
}

// TestFixtureReparseSecondPassTakesOver: 最高清晰度文档直接生效，轨道取自它。
//
// 本仓优化（docs/ROADMAP.md O2）：上游是「先 qn=0、再 qn=127 重发」，重发带 dash.video
// 就整份取代前者——常见情况下第一份 qn=0 文档白发。本仓改成 qn=127 优先，因此这里只见
// 一次请求；落点与上游一致（都是 pass2 那份文档），所以对结果的断言不变。
func TestFixtureReparseSecondPassTakesOver(t *testing.T) {
	res, qns, err := extractFixturePasses(t, "dash-reparse-pass1", "dash-reparse-pass2")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(qns) != 1 || qns[0] != maxQn {
		t.Fatalf("qn sequence = %v, want [%s]（最高清晰度可用时不再发第二遍）", qns, maxQn)
	}
	if len(res.VideoTracks) != 1 || res.VideoTracks[0].ID != "127" {
		t.Errorf("video tracks = %+v, want exactly the 127 track from the second pass", res.VideoTracks)
	}
	if !strings.Contains(res.WebJSONString, "repass-127.m4s") {
		t.Error("WebJSONString must point at the document that took over")
	}
}

// TestFixtureDurlReplayFallsBackOnRefusal: 最高清晰度那份不带 dash.video 时，回落默认清晰度，
// 落点仍是「默认那份文档」（等价上游「保留第一份」的结论，只是回落方向相反）。
func TestFixtureDurlReplayFallsBackOnRefusal(t *testing.T) {
	res, qns, err := extractFixturePasses(t, "durl-replay-first", "durl-replay-empty")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(qns) != 2 || qns[0] != maxQn || qns[1] != "0" {
		t.Fatalf("qn sequence = %v, want [%s 0]（最高清晰度被拒后回落默认）", qns, maxQn)
	}
	if len(res.Clips) != 2 || res.Clips[0] != "https://upos.example.com/replay-seg1.flv" {
		t.Errorf("clips = %v, want the first pass's durl segments", res.Clips)
	}
	if !strings.Contains(res.WebJSONString, "replay-seg1.flv") {
		t.Error("the first document must remain the one in effect")
	}
}

// TestFixtureFlvDurlStillReparses: FLV/durl 稿件没有 dash.video，所以「最高清晰度优先」照样会
// 落回默认清晰度——两次请求与上游相同，省不下（这也正是优化只对 DASH 生效的边界）。
func TestFixtureFlvDurlStillReparses(t *testing.T) {
	res, qns, err := extractFixturePasses(t, "flv-durl", "flv-durl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(qns) != 2 || qns[0] != maxQn || qns[1] != "0" {
		t.Errorf("qn sequence = %v, want exactly [%s 0]", qns, maxQn)
	}
	if len(res.Clips) != 2 {
		t.Errorf("clips = %v, want the 2 durl segments", res.Clips)
	}
}

// extractFixtureWithEpid is extractFixture plus the episode id, which routes the
// parse through the bangumi playurl endpoint.
func extractFixtureWithEpid(t *testing.T, fixture, epID string) (*entity.ParsedResult, error) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", fixture+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", fixture, err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.EpHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	cfg.Cookie = ""
	cfg.Token = ""

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	return NewParser(client, cfg).ExtractTracks(context.Background(), "ep:"+epID, "170001", "999", epID, false, false, false, "", false, "0")
}

// TestFixtureBangumiWebDashParsesTracks: the pgc playurl response carries the
// tracks under a "result" root rather than "data" — a shape the shared parse
// must still map (upstream F02).
func TestFixtureBangumiWebDashParsesTracks(t *testing.T) {
	res, err := extractFixtureWithEpid(t, "bangumi-web-dash-result", "307930")
	if err != nil {
		t.Fatalf("bangumi parse: %v", err)
	}
	if len(res.VideoTracks) != 1 {
		t.Fatalf("video tracks = %d, want 1", len(res.VideoTracks))
	}
	if res.VideoTracks[0].ID != "80" {
		t.Errorf("video id = %q, want 80", res.VideoTracks[0].ID)
	}
	if len(res.AudioTracks) != 1 {
		t.Fatalf("audio tracks = %d, want 1", len(res.AudioTracks))
	}
	if res.AudioTracks[0].ID != "30216" {
		t.Errorf("audio id = %q, want 30216", res.AudioTracks[0].ID)
	}
}

// TestFixtureBangumiWebDashVideoInfoRoot: the same pgc endpoint also answers
// with the tracks nested under "result.video_info" instead of a bare "result".
// Both shapes must locate the track list the same way (upstream F02a/F02b), and
// the bangumi path must not be WBI-signed.
func TestFixtureBangumiWebDashVideoInfoRoot(t *testing.T) {
	res, err := extractFixtureWithEpid(t, "bangumi-web-dash-video-info", "307930")
	if err != nil {
		t.Fatalf("bangumi parse: %v", err)
	}
	if len(res.VideoTracks) != 1 {
		t.Fatalf("video tracks = %d, want 1", len(res.VideoTracks))
	}
	if res.VideoTracks[0].ID != "116" {
		t.Errorf("video id = %q, want 116", res.VideoTracks[0].ID)
	}
	if res.VideoTracks[0].Dfn != "1080P 高帧率" {
		t.Errorf("video dfn = %q, want 1080P 高帧率", res.VideoTracks[0].Dfn)
	}
}

// TestFixturePlayLimitedReportsReason: a play_check with a limit reason must
// surface as a readable error naming both the machine reason and the
// human-readable detail, instead of an empty-track "success" (upstream F13).
func TestFixturePlayLimitedReportsReason(t *testing.T) {
	_, err := extractFixtureWithEpid(t, "play-limited", "307930")
	if err == nil {
		t.Fatal("a VIP-limited response must be reported as an error, not an empty track list")
	}
	for _, want := range []string{"大会员", "limit_play_reason=VIP_LIMIT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %q", err.Error(), want)
		}
	}
}

// extractIntlFixture drives the international parse path against a server that
// routes by the prefer_code_type query parameter, which is how upstream's
// FakeBilibiliApiServer distinguishes the two passes.
func extractIntlFixture(t *testing.T, routes map[string]string) (*entity.ParsedResult, []string, error) {
	t.Helper()

	var seen []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("prefer_code_type")
		seen = append(seen, key)
		name, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := os.ReadFile(filepath.Join("testdata", name+".json"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	cfg.Cookie = ""
	cfg.Token = ""

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	res, err := NewParser(client, cfg).ExtractTracks(context.Background(), "av170001", "170001", "999", "", false, true, false, "", false, "0")
	return res, seen, err
}

// TestFixtureIntlMergesTwoPassStreamLists: the international endpoint is queried
// twice (prefer_code_type=0 then 1) and both stream lists are merged, so a codec
// offered only in the second pass stays selectable.
func TestFixtureIntlMergesTwoPassStreamLists(t *testing.T) {
	res, seen, err := extractIntlFixture(t, map[string]string{"0": "intl-code0", "1": "intl-code1"})
	if err != nil {
		t.Fatalf("intl parse: %v", err)
	}
	if len(seen) != 2 || seen[0] != "0" || seen[1] != "1" {
		t.Errorf("prefer_code_type sequence = %v, want [0 1]", seen)
	}
	if len(res.VideoTracks) != 2 {
		t.Errorf("video tracks = %d, want 2 (one per pass)", len(res.VideoTracks))
	}
	if len(res.AudioTracks) != 2 {
		t.Errorf("audio tracks = %d, want 2 (one per pass)", len(res.AudioTracks))
	}
}
