package entity

import (
	"strings"
	"testing"
)

// 上游 BilibiliBvConverterTests 的编码/解码表。
func TestUpstreamBvConverter(t *testing.T) {
	encode := []struct {
		aid  int64
		want string
	}{
		{170001, "BV17x411w7KC"},
		{455017605, "BV1Q541167Qg"},
		{882584971, "BV1mK4y1C7Bz"},
	}
	for _, c := range encode {
		got, err := AvToBv(c.aid)
		if err != nil {
			t.Errorf("AvToBv(%d) 报错: %v", c.aid, err)
			continue
		}
		if got != c.want {
			t.Errorf("AvToBv(%d) = %q, 上游期望 %q", c.aid, got, c.want)
		}
	}

	decode := []struct {
		in   string
		want int64
	}{
		{"7x411w7KC", 170001},
		{"Q541167Qg", 455017605},
		{"mK4y1C7Bz", 882584971},
		// 带前缀的形式由调用方剥离后再进本函数（上游 DecodeBv 在解析层剥前缀，
		// 本仓由 workflow.resolveBv 剥）；前缀的宽容性在 resolve 的差分用例里断言。
	}
	for _, c := range decode {
		got, err := BvToAv(c.in)
		if err != nil {
			t.Errorf("BvToAv(%q) 报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("BvToAv(%q) = %d, 上游期望 %d", c.in, got, c.want)
		}
	}

	// 上游：过小的 aid、长度不对、含非法字符都要报错
	if got, err := AvToBv(0); err == nil {
		t.Errorf("AvToBv(0) = %q，上游要求报错", got)
	}
	if got, err := BvToAv("7x411w7K"); err == nil {
		t.Errorf("BvToAv(长度不对) = %d，上游要求报错", got)
	}
	if got, err := BvToAv("7x411w7K!"); err == nil {
		t.Errorf("BvToAv(含非法字符) = %d，上游要求报错", got)
	}

	if !strings.HasPrefix(mustAvToBv(t, 170001), "BV1") {
		t.Error("编码结果应以 BV1 开头")
	}
}

func mustAvToBv(t *testing.T, aid int64) string {
	t.Helper()
	bv, err := AvToBv(aid)
	if err != nil {
		t.Fatalf("AvToBv(%d): %v", aid, err)
	}
	return bv
}
