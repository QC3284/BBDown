package workflow

import (
	"reflect"
	"testing"
)

func TestParsePageSelection(t *testing.T) {
	cases := []struct {
		expr    string
		want    []string
		wantErr bool
	}{
		{"1,3,5", []string{"1", "3", "5"}, false},
		{"1-3", []string{"1", "2", "3"}, false},
		{"1-3,7,9-11", []string{"1", "2", "3", "7", "9", "10", "11"}, false},
		{"5", []string{"5"}, false},
		{"10-1", nil, true}, // start > end must abort (upstream)
		{"1-3-5", nil, true},
		{"abc", nil, true},
		{"-5", nil, true}, // invalid: clear error instead of upstream token quirk
		{"1,,2", []string{"1", "2"}, false},
	}
	for _, c := range cases {
		got, err := parsePageSelection(c.expr)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePageSelection(%q) expected error, got %v", c.expr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePageSelection(%q) unexpected error: %v", c.expr, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePageSelection(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestParsePageSelectionMaxExpanded(t *testing.T) {
	_, err := parsePageSelection("1-1000001")
	if err == nil {
		t.Fatal("expected expansion limit error")
	}
}

func TestParseEncodingPriority(t *testing.T) {
	m, first := parseEncodingPriority("hevc,avc,av1")
	if first != "HEVC" {
		t.Errorf("first = %q, want HEVC", first)
	}
	if m["HEVC"] != 0 || m["AVC"] != 1 || m["AV1"] != 2 {
		t.Errorf("map = %v", m)
	}
	// Earlier = higher priority: HEVC(0) < AVC(1).

	// Chinese comma, dashes removed (dots kept, matching upstream), dedup.
	m, first = parseEncodingPriority("H.264，hevc,-av1-,hevc")
	if first != "H.264" {
		t.Errorf("first = %q, want H.264", first)
	}
	if m["H.264"] != 0 || m["HEVC"] != 1 || m["AV1"] != 2 {
		t.Errorf("map = %v", m)
	}
}

func TestParseDfnPriority(t *testing.T) {
	m := parseDfnPriority("8K, 1080P 高码率，720P 高清")
	if m["8K"] != 0 || m["1080P 高码率"] != 1 || m["720P 高清"] != 2 {
		t.Errorf("map = %v", m)
	}
}

func TestParseDanmakuFormats(t *testing.T) {
	got, err := parseDanmakuFormats("")
	if err != nil || !reflect.DeepEqual(got, []string{"xml", "ass"}) {
		t.Errorf("default = %v, %v", got, err)
	}
	got, _ = parseDanmakuFormats("XML，ass")
	if !reflect.DeepEqual(got, []string{"xml", "ass"}) {
		t.Errorf("formats = %v", got)
	}
	got, _ = parseDanmakuFormats("bogus")
	if !reflect.DeepEqual(got, []string{"xml", "ass"}) {
		t.Errorf("invalid format should fall back to defaults, got %v", got)
	}
}
