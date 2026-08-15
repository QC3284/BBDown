package workflow

import (
	"context"
	"testing"
)

// TestResolveURLPureRules covers resolution branches that never touch the
// network (nil HTTP client is fine for these).
func TestResolveURLPureRules(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		in   string
		want string
	}{
		{"av170001", "170001"},
		{"av:170001", "170001"},
		{"BV17x411w7KC", "170001"},
		{"bv:7x411w7KC", "170001"}, // 9-char suffix after "BV1"
		{"ep838839", "ep:838839"},
		{"https://www.bilibili.com/bangumi/play/ep838839", "ep:838839"},
		{"https://www.bilibili.com/video/av170001", "170001"},
		{"https://space.bilibili.com/23630128", "mid:23630128"},
		{"https://space.bilibili.com/23630128/favlist?fid=123", "favId:123:23630128"},
		{"https://space.bilibili.com/23630128/channel/collectiondetail?sid=2045", "listBizId:2045"},
		{"https://space.bilibili.com/23630128/channel/seriesdetail?sid=340933", "seriesBizId:340933"},
		{"https://space.bilibili.com/23630128/lists/12345", "listBizId:12345"},
		{"https://space.bilibili.com/23630128/lists/12345?type=series", "seriesBizId:12345"},
		{"https://www.bilibili.com/medialist/play/1?business=space_collection&business_id=2045", "listBizId:2045"},
		{"https://www.bilibili.com/medialist/play/1?business=space_series&business_id=340933", "seriesBizId:340933"},
		{"https://www.bilibili.com/list/watchlater?ep_id=123456", "ep:123456"},
		{"https://www.bilibili.tv/en/play/12345/123456", "ep:123456"},
	}
	for _, c := range cases {
		t.Logf("case: %q", c.in)
		got, err := ResolveURL(ctx, nil, c.in)
		if err != nil {
			t.Errorf("ResolveURL(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveURLInvalidBV(t *testing.T) {
	_, err := ResolveURL(context.Background(), nil, "BV1invalid123")
	if err == nil {
		t.Fatal("invalid BV should error")
	}
}
