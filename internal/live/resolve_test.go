package live

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

const flvPayload = `{"code":0,"data":{"playurl_info":{"playurl":{"stream":[{"format":[{"format_name":"flv","codec":[{"base_url":"/live/stream.flv","url_info":[{"host":"https://cdn.example.com","extra":"?token=1"}]}]}]}]}}}}`

// TestResolveLiveFallsBackToLowerQuality: qn=30000 asks for the highest tier the
// account is entitled to, but an unprivileged session may be offered nothing
// recordable at that tier. The request must then be repeated once at qn=10000
// instead of failing the recording (upstream v1.6.13).
func TestResolveLiveFallsBackToLowerQuality(t *testing.T) {
	var qns []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "get_info"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"title":"房间","uname":"UP","live_status":1}}`)
		case strings.Contains(r.URL.Path, "getRoomPlayInfo"):
			qn := r.URL.Query().Get("qn")
			qns = append(qns, qn)
			if qn == "30000" {
				_, _ = io.WriteString(w, `{"code":0,"data":{"playurl_info":{"playurl":{"stream":[]}}}}`)
				return
			}
			_, _ = io.WriteString(w, flvPayload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	orig := liveAPIBase
	liveAPIBase = srv.URL
	defer func() { liveAPIBase = orig }()

	client := util.NewHTTPClient(nil, func() string { return "" }, nil)
	url, title, uname, err := ResolveLive(context.Background(), "12345", client)
	if err != nil {
		t.Fatalf("ResolveLive: %v", err)
	}
	if url != "https://cdn.example.com/live/stream.flv?token=1" {
		t.Errorf("stream URL = %q", url)
	}
	if title != "房间" || uname != "UP" {
		t.Errorf("title/uname = %q/%q", title, uname)
	}
	if len(qns) != 2 || qns[0] != "30000" || qns[1] != "10000" {
		t.Errorf("quality requests = %v, want [30000 10000]", qns)
	}
}

// TestResolveLiveUsesHighQualityWhenAvailable pins the fast path: no fallback
// request when qn=30000 already yields a stream.
func TestResolveLiveUsesHighQualityWhenAvailable(t *testing.T) {
	var qns []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "get_info") {
			_, _ = io.WriteString(w, `{"code":0,"data":{"title":"房间","uname":"UP","live_status":1}}`)
			return
		}
		qns = append(qns, r.URL.Query().Get("qn"))
		_, _ = io.WriteString(w, flvPayload)
	}))
	defer srv.Close()

	orig := liveAPIBase
	liveAPIBase = srv.URL
	defer func() { liveAPIBase = orig }()

	client := util.NewHTTPClient(nil, func() string { return "" }, nil)
	if _, _, _, err := ResolveLive(context.Background(), "12345", client); err != nil {
		t.Fatalf("ResolveLive: %v", err)
	}
	if len(qns) != 1 || qns[0] != "30000" {
		t.Errorf("quality requests = %v, want exactly [30000]", qns)
	}
}
