package util

import "testing"

// 本文件搬上游 BuvidProviderTests：buvid3 只应在缺失时补充，
// 检测又必须大小写不敏感、且不能把 buvid4 / buvid 之类误判成已具备。

func TestUpstreamHasBuvid3(t *testing.T) {
	cases := []struct {
		cookie string
		want   bool
	}{
		{"buvid3=ABC123", true},
		{"SESSDATA=x;buvid3=ABC123", true},
		{"buvid3=ABC123;SESSDATA=x", true},
		{"BUVID3=ABC123", true}, // B 站自身大小写并不统一
		{"SESSDATA=x", false},
		{"", false},
		{"buvid4=ABC", false}, // 相似键不能误判
		{"buvid=ABC", false},
	}
	for _, c := range cases {
		if got := HasBuvid3(c.cookie); got != c.want {
			t.Errorf("HasBuvid3(%q) = %v，上游期望 %v", c.cookie, got, c.want)
		}
	}
}
