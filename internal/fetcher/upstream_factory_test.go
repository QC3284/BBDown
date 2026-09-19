package fetcher

import (
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// 本文件搬上游 FetcherFactoryTests 的分发表：一组目标标识必须落到各自的 fetcher。

func TestUpstreamFetcherFactoryRouting(t *testing.T) {
	client := util.NewHTTPClient(nil, func() string { return "" }, nil)
	f := NewFactory(client, false, "wbi", "cookie", "api.bilibili.com", "api.bilibili.com", "token")

	check := func(target string, want any) {
		t.Helper()
		got := f.Create(target)
		switch want.(type) {
		case *SpaceVideoFetcher:
			if _, ok := got.(*SpaceVideoFetcher); !ok {
				t.Errorf("%q → %T，上游期望 SpaceVideoFetcher", target, got)
			}
		case *FavListFetcher:
			if _, ok := got.(*FavListFetcher); !ok {
				t.Errorf("%q → %T，上游期望 FavListFetcher", target, got)
			}
		case *MediaListFetcher:
			if _, ok := got.(*MediaListFetcher); !ok {
				t.Errorf("%q → %T，上游期望 MediaListFetcher", target, got)
			}
		case *SeriesListFetcher:
			if _, ok := got.(*SeriesListFetcher); !ok {
				t.Errorf("%q → %T，上游期望 SeriesListFetcher", target, got)
			}
		case *BangumiInfoFetcher:
			if _, ok := got.(*BangumiInfoFetcher); !ok {
				t.Errorf("%q → %T，上游期望 BangumiInfoFetcher", target, got)
			}
		case *CheeseInfoFetcher:
			if _, ok := got.(*CheeseInfoFetcher); !ok {
				t.Errorf("%q → %T，上游期望 CheeseInfoFetcher", target, got)
			}
		case *NormalInfoFetcher:
			if _, ok := got.(*NormalInfoFetcher); !ok {
				t.Errorf("%q → %T，上游期望 NormalInfoFetcher", target, got)
			}
		}
	}

	check("mid:12345", (*SpaceVideoFetcher)(nil))
	check("favId:1:2", (*FavListFetcher)(nil))
	check("listBizId:123", (*MediaListFetcher)(nil))
	check("seriesBizId:123", (*SeriesListFetcher)(nil))
	check("ep:12345", (*BangumiInfoFetcher)(nil))
	check("cheese:12345", (*CheeseInfoFetcher)(nil))
	check("170001", (*NormalInfoFetcher)(nil))

	// ep + 国际版 → IntlBangumiInfoFetcher
	intl := NewFactory(client, true, "wbi", "cookie", "api.bilibili.com", "api.bilibili.com", "token")
	if _, ok := intl.Create("ep:12345").(*IntlBangumiInfoFetcher); !ok {
		t.Error("ep + --use-intl-api 应路由到 IntlBangumiInfoFetcher")
	}

	// 订阅目标（sub add 的输入形态）都不能落到 NormalInfoFetcher
	for _, target := range []string{"mid:1", "favId:1:2", "listBizId:1", "seriesBizId:1", "ep:1", "cheese:1"} {
		if _, ok := f.Create(target).(*NormalInfoFetcher); ok {
			t.Errorf("%q 不应落到 NormalInfoFetcher", target)
		}
	}
}
