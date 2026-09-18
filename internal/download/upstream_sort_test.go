package download

import (
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// 上游 TrackSortTests：id 是服务器可控字符串，缺失/畸形时排序必须降级为 0，
// 而不是抛异常（上游曾因裸 Convert.ToInt32 让单个畸形节点中止整批多P下载）。
func TestUpstreamSortTracksMalformedID(t *testing.T) {
	tracks := []entity.Video{
		{ID: "", Codecs: "avc", Dfn: "1080P 高码率", Bandwidth: 100},
		{ID: "not-a-number", Codecs: "avc", Dfn: "1080P 高码率", Bandwidth: 200},
		{ID: "999999999999", Codecs: "avc", Dfn: "1080P 高码率", Bandwidth: 50},
		{ID: "80", Codecs: "avc", Dfn: "1080P 高码率", Bandwidth: 10},
	}
	dfnPriority := map[string]int{"1080P 高码率": 1}
	encodingPriority := map[string]int{"avc": 1}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("畸形 id 让排序 panic（上游要求降级为 0）: %v", r)
		}
	}()
	sorted := SortVideoTracks(tracks, dfnPriority, encodingPriority, false)

	if len(sorted) != 4 {
		t.Fatalf("排序后 %d 条，上游期望 4 条", len(sorted))
	}
	if sorted[0].ID != "80" {
		t.Errorf("排序首位是 %q，上游期望唯一可解析且最大的 id(80)", sorted[0].ID)
	}
	// 其余三条 id 均降级为 0（并列），按带宽降序：200 → 100 → 50
	want := []string{"not-a-number", "", "999999999999"}
	for i, w := range want {
		if sorted[i+1].ID != w {
			t.Errorf("第 %d 位是 %q，上游期望 %q（带宽降序）", i+1, sorted[i+1].ID, w)
		}
	}
}
