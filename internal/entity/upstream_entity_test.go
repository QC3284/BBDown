package entity

import "testing"

// 本文件搬上游 EntityTests 的两张表：Page.Bvid 的越界回落与 Audio 的短编码名。

func TestUpstreamPageBvid(t *testing.T) {
	// 正常 aid 编码成 BV
	if got := (Page{Aid: "170001"}).Bvid(); got != "BV17x411w7KC" {
		t.Errorf("aid 170001 → %q，上游期望 BV17x411w7KC", got)
	}

	// Encode 的范围校验（下界 1、上界 2^51）不满足时回落原始 aid，而不是给空串
	for _, aid := range []string{"0", "-5", "2251799813685248"} {
		if got := (Page{Aid: aid}).Bvid(); got != aid {
			t.Errorf("aid %q → %q，上游期望回落原始 aid", aid, got)
		}
	}

	// 非数字 aid 原样返回（bvid 形态的 aid）
	if got := (Page{Aid: "BV1xx411c7mD"}).Bvid(); got != "BV1xx411c7mD" {
		t.Errorf("非数字 aid → %q，上游期望原样返回", got)
	}
}

func TestUpstreamAudioShortCodecs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"avc", "AVC"},
		{"ec-3", "EC3"},
		{"mp4a.40.2", "MP4A.40.2"},
		{"E-AC-3", "EAC3"},
	}
	for _, c := range cases {
		if got := (Audio{Codecs: c.in}).ShortCodecs(); got != c.want {
			t.Errorf("ShortCodecs(%q) = %q，上游期望 %q", c.in, got, c.want)
		}
	}
}
