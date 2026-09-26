package workflow

import (
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
)

// --compat：避开 HDR Vivid(129) / 杜比视界(126)，但绝不把候选清空。
//
// 变异验证：把过滤条件改成恒 false（不过滤）→ 第一条断言变红；改成「无脑全滤」→ 第二条变红。
func TestFilterCompatTracks(t *testing.T) {
	mk := func(ids ...string) []entity.Video {
		var out []entity.Video
		for _, id := range ids {
			out = append(out, entity.Video{ID: id, Dfn: config.QualityMap[id]})
		}
		return out
	}

	got := filterCompatTracks(mk(config.HDRVividID, "126", "127", "80"))
	if len(got) != 2 || got[0].ID != "127" || got[1].ID != "80" {
		t.Errorf("应当滤掉 129/126 且保持顺序，实际 %+v", got)
	}

	// 只有 HDR/杜比视界档位时：原样返回（不能把用户逼到「无候选」）。
	only := mk(config.HDRVividID, "126")
	if got := filterCompatTracks(only); len(got) != 2 {
		t.Errorf("全是 HDR/杜比视界时应当原样返回，实际 %+v", got)
	}
	if got := filterCompatTracks(nil); got != nil {
		t.Errorf("空输入应当原样返回，实际 %+v", got)
	}
}
