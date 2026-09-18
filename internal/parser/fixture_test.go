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

// TestFixtureReparseProtocol parks the "no-recompress re-request" protocol.
//
// Upstream re-issues the UGC playurl request with qn=127 after a qn=0 pass and
// lets the second response take over (dash-reparse-pass1/2), falling back to the
// validated first response when the re-request is refused (durl-replay-first +
// durl-replay-empty). The Go port has none of that: it issues a single request,
// so flv-durl (expects 2 requests, the second carrying qn=127) and the four
// fixtures below cannot pass yet.
//
// This is the concrete form of decision 1 in docs/UPSTREAM_ALIGNMENT.md §4.5 —
// either implement the protocol or record it as a deliberate downgrade. The
// assertions are already written so implementing it only means deleting the Skip.
func TestFixtureReparseProtocol(t *testing.T) {
	t.Skip("免二压重发协议未实现：见 docs/UPSTREAM_ALIGNMENT.md §4.5 待决策 1")

	// first, second := multiRequestFixture(t, "dash-reparse-pass1", "dash-reparse-pass2")
	// if first+second != 2 {
	// 	t.Fatalf("requests = %d, want 2 (qn=0 then qn=127)", first+second)
	// }
	// assert second request query carries qn=127 and its response took over
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
