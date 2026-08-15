package util

import "testing"

func TestConvertSubFromJSON(t *testing.T) {
	jsonStr := `{"body":[{"from":1.5,"to":3.2,"content":"第一行"},{"from":4,"to":5.5,"content":"第二行"}]}`
	want := "1\n" + "00:00:01,500 --> 00:00:03,200\n" + "第一行\n\n" +
		"2\n" + "00:00:04,000 --> 00:00:05,500\n" + "第二行\n\n"
	if got := ConvertSubFromJSON(jsonStr); got != want {
		t.Errorf("ConvertSubFromJSON =\n%q\nwant\n%q", got, want)
	}
	// Invalid JSON returns empty string.
	if got := ConvertSubFromJSON("{invalid"); got != "" {
		t.Errorf("invalid json should produce empty, got %q", got)
	}
}

func TestFormatSubTime(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "00:00:00,000"},
		{61.25, "00:01:01,250"},
		{3661.999, "01:01:01,998"}, // float64 precision truncates 999ms
		{-5, "00:00:00,000"},
	}
	for _, c := range cases {
		if got := FormatSubTime(c.sec); got != c.want {
			t.Errorf("FormatSubTime(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}

func TestSubCode2(t *testing.T) {
	cases := map[string]string{
		"zh-CN": "chi",
		"en-US": "eng",
		"ja":    "jpn",
		"ko":    "kor",
		"xx-YY": "und", // fallback (upstream Undetermined)
	}
	for k, want := range cases {
		if got := SubCode2(k); got != want {
			t.Errorf("SubCode2(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestGetSubtitleCodeDisplayNames(t *testing.T) {
	cases := map[string][2]string{
		"zh-CN":   {"chi", "中文（简体）"},
		"zh-hans": {"chi", "中文（简体）"}, // normalized zh-hans => zh-Hans
		"en-US":   {"eng", "English(USA)"},
		"ja":      {"jpn", "日本語"},
		"ai-Zh":   {"chi", "中文（简体, AI识别）"},
	}
	for k, want := range cases {
		code, name := GetSubtitleCode(k)
		if code != want[0] || name != want[1] {
			t.Errorf("GetSubtitleCode(%q) = (%q, %q), want (%q, %q)", k, code, name, want[0], want[1])
		}
	}
}

func TestSanitizeSRT(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb\nc", "a\nb\nc"},
		{"  padded  \n", "  padded"},
		{"", ""},
	}
	for _, c := range cases {
		if got := SanitizeSRT(c.in); got != c.want {
			t.Errorf("SanitizeSRT(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
