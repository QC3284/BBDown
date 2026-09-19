package util

import (
	"strings"
	"testing"
)

// 本文件把上游 BBDown.Tests/SensitiveDataMaskerTests.cs 的断言整表搬过来做差分。
// 脱敏是安全相关的用户可见行为：少掩一个键，签名/票据就会明文落进日志文件。

func TestUpstreamMaskValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", "***"},
		{"12345678", "***"},
		{"123456789", "1234***6789"},
		{"abcdefghijklmnop", "abcd***mnop"},
	}
	for _, c := range cases {
		if got := MaskValue(c.in); got != c.want {
			t.Errorf("MaskValue(%q) = %q, 上游期望 %q", c.in, got, c.want)
		}
	}
}

func TestUpstreamMaskUrl(t *testing.T) {
	// 上游 MaskUrl_MasksAccessKeyButKeepsOtherParams：逐字比对（其余参数不得改动）
	const in = "https://api.bilibili.com/x/player?access_key=abcdefghijklmnop&cid=123&qn=80"
	const want = "https://api.bilibili.com/x/player?access_key=abcd***mnop&cid=123&qn=80"
	if got := MaskUrl(in); got != want {
		t.Errorf("MaskUrl 逐字比对失败\n  got  %q\n  want %q", got, want)
	}

	// 三个 token 类键都要掩
	if got := MaskUrl("https://x.com/a?access_token=aaaaaaaaaaaa&refresh_token=bbbbbbbbbbbb&token=cccccccccccc"); got != "" {
		for _, leaked := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"} {
			if strings.Contains(got, leaked) {
				t.Errorf("MaskUrl 漏出 %q：%s", leaked, got)
			}
		}
	}

	// 上游 MaskUrl_MasksSignedMediaUrlParams（B3-F1）：CDN 临时授权参数必须脱敏
	signed := MaskUrl("https://cn-hnbc-dx-v-12.bilivideo.com/upgcx/1.mp4?deadline=1750000000&sign=deadbeefdeadbeefdeadbeef&x_sign=aaabbbcccddd&marlin_token=xyz12345678")
	for _, leaked := range []string{"deadbeefdeadbeefdeadbeef", "aaabbbcccddd", "1750000000", "xyz12345678"} {
		if strings.Contains(signed, leaked) {
			t.Errorf("签名媒体 URL 漏出 %q：%s", leaked, signed)
		}
	}
	for _, kept := range []string{"sign=", "deadline="} {
		if !strings.Contains(signed, kept) {
			t.Errorf("应保留键名便于定位，缺少 %q：%s", kept, signed)
		}
	}

	// 无 query 的 URL 原样返回
	const plain = "https://www.bilibili.com/video/BV1qt4y1X7TW"
	if got := MaskUrl(plain); got != plain {
		t.Errorf("无 query 的 URL 应原样返回：%q", got)
	}

	// fragment 保留
	frag := MaskUrl("https://x.com/a?access_key=abcdefghijkl#section")
	if !strings.HasSuffix(frag, "#section") {
		t.Errorf("fragment 丢失：%q", frag)
	}
	if strings.Contains(frag, "abcdefghijkl") {
		t.Errorf("access_key 漏出：%q", frag)
	}
}

func TestUpstreamMaskCookie(t *testing.T) {
	masked := MaskCookie("SESSDATA=abcdefghijklmnop; buvid3=xyz; bili_jct=1234567890ab")
	if !strings.Contains(masked, "SESSDATA=abcd***mnop") {
		t.Errorf("SESSDATA 未按首尾各 4 位脱敏：%q", masked)
	}
	// 上游只掩 SensitiveKeys 里的键：buvid3 不是凭据，保留原文便于排查
	if !strings.Contains(masked, "buvid3=xyz") {
		t.Errorf("buvid3 不是敏感键，不应被掩：%q", masked)
	}
	for _, leaked := range []string{"abcdefghijklmnop", "1234567890ab"} {
		if strings.Contains(masked, leaked) {
			t.Errorf("Cookie 漏出 %q：%s", leaked, masked)
		}
	}

	// 值里含 '=' 时不能二次分割导致尾部漏出
	if got := MaskCookie("SESSDATA=abcd=efgh=ijklmnop"); strings.Contains(got, "ijklmnop") {
		t.Errorf("含 = 的值漏出尾部：%q", got)
	}
}
