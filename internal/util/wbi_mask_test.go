package util

import "testing"

func TestGetMixinKey(t *testing.T) {
	// 64-char img_key+sub_key: the mixin table picks 32 chars at fixed indices.
	orig := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ab"
	key := GetMixinKey(orig)
	if len(key) != 32 {
		t.Fatalf("mixin key length = %d, want 32", len(key))
	}
	// Verify against the table positions.
	want := ""
	for _, idx := range mixinKeyEncTab {
		want += string(orig[idx])
	}
	if key != want {
		t.Fatalf("mixin key = %q, want %q", key, want)
	}
}

func TestRSubString(t *testing.T) {
	cases := map[string]string{
		"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png": "7cd084941338484aae1ad9425b84077c",
		"no-extension": "no-extension",
	}
	for in, want := range cases {
		if got := RSubString(in); got != want {
			t.Errorf("RSubString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSubtitleLangKey(t *testing.T) {
	cases := map[string]string{
		"zh-hans": "zh-Hans",
		"zh-CN":   "zh-CN",
		"en-us":   "en-Us", // only the char after "-" is uppercased (upstream)
		"ja":      "ja",
	}
	for in, want := range cases {
		if got := normalizeSubtitleLangKey(in); got != want {
			t.Errorf("normalizeSubtitleLangKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskValue(t *testing.T) {
	if MaskValue("") != "" {
		t.Error("empty should stay empty")
	}
	if MaskValue("short") != "***" {
		t.Error("short should become ***")
	}
	if MaskValue("abcdefgh") != "***" {
		t.Error("8 chars should become ***")
	}
	if MaskValue("abcdefghijklmn") != "abcd***klmn" {
		t.Errorf("long mask = %q", MaskValue("abcdefghijklmn"))
	}
}

func TestMaskUrl(t *testing.T) {
	got := MaskUrl("https://api.bilibili.com/x/playurl?access_key=SECRET123456&cid=1")
	if got == "" || containsPlain(got, "SECRET123456") {
		t.Errorf("MaskUrl leaked secret: %q", got)
	}
}

func TestMaskCookie(t *testing.T) {
	got := MaskCookie("SESSDATA=abcdefghijklmn; bili_jct=short; buvid3=xyz")
	if containsPlain(got, "abcdefghijklmn") {
		t.Errorf("MaskCookie leaked SESSDATA: %q", got)
	}
}

func containsPlain(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
