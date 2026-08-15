package download

import "testing"

func TestForceHTTPIfNeeded(t *testing.T) {
	cases := []struct {
		in    string
		force bool
		want  string
	}{
		{"https://upos.example.com/a.mp4", false, "https://upos.example.com/a.mp4"},
		{"https://upos.example.com/a.mp4", true, "http://upos.example.com/a.mp4"},
		{"https://xy.mcdn.bilivideo.cn:4483/a.mp4", true, "https://xy.mcdn.bilivideo.cn:4483/a.mp4"}, // mcdn never downgraded
		{"http://upos.example.com/a.mp4", true, "http://upos.example.com/a.mp4"},
	}
	for _, c := range cases {
		if got := forceHTTPIfNeeded(c.in, c.force); got != c.want {
			t.Errorf("forceHTTPIfNeeded(%q, %v) = %q, want %q", c.in, c.force, got, c.want)
		}
	}
}

func TestRangeStart(t *testing.T) {
	cases := map[string]int64{
		"bytes 100-199/1000": 100,
		"bytes 0-99/1000":    0,
		"":                   -1,
		"bytes -100":         -1,
	}
	for in, want := range cases {
		if got := rangeStart(in); got != want {
			t.Errorf("rangeStart(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestClipPath(t *testing.T) {
	if got := clipPath("/tmp/video.mp4", 3); got != "/tmp/video.mp4.003.vclip" {
		t.Errorf("clipPath = %q", got)
	}
}

func TestSegmentSizeDefaults(t *testing.T) {
	cfg := DownloadConfig{}
	if cfg.segmentSize() != 20 || cfg.retryCount() != 3 {
		t.Error("default segment/retry config wrong")
	}
	cfg.SegmentSizeMB = 5
	cfg.RetryCount = 7
	if cfg.segmentSize() != 5 || cfg.retryCount() != 7 {
		t.Error("configured segment/retry config wrong")
	}
}
