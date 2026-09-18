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
