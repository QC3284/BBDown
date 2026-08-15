package live

import "testing"

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"正常标题":          "正常标题",
		"a/b\\c:d*e?f":  "a_b_c_d_e_f",
		"line\nbreak\r": "line_break_",
		"\t控制字符\x01":    "_控制字符_",
		"":              "直播",
		"   ":           "直播",
	}
	for in, want := range cases {
		if got := SanitizeFileName(in); got != want {
			t.Errorf("SanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
