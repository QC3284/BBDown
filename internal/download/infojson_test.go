package download

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// --info-json：把解析结果交给程序。字段名与仓库其它 JSON 输出一致（snake_case），空列表写 [] 而不是 null。
//
// 变异验证：把 Video 的 nil 兜底删掉 → 「空轨道必须是 [] 而不是 null」断言变红。
func TestRenderInfoJSON(t *testing.T) {
	out, err := RenderInfoJSON(InfoPayload{
		Title: "标题", Bvid: "BV1", Aid: "1", Cid: "2", PageIndex: 3, PageTitle: "P3", DurationSec: 100,
		Video: []entity.Video{{ID: "80", Dfn: "1080P 高清", Codecs: "avc1.640032", Bandwidth: 2000000, Res: "1920x1080"}},
		Audio: []entity.Audio{{ID: "30280", Codecs: "mp4a.40.2", Bandwidth: 64000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("输出应当以换行结尾（便于按行读取）")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("输出必须是合法 JSON：%v\n%s", err, out)
	}
	for k, want := range map[string]interface{}{
		"title": "标题", "bvid": "BV1", "aid": "1", "cid": "2", "page_title": "P3",
	} {
		if got[k] != want {
			t.Errorf("%s = %v，期望 %v", k, got[k], want)
		}
	}
	if got["page_index"] != float64(3) || got["duration_sec"] != float64(100) {
		t.Errorf("分P序号/时长不符：%v / %v", got["page_index"], got["duration_sec"])
	}
	if v, ok := got["video"].([]interface{}); !ok || len(v) != 1 {
		t.Errorf("video 应当是 1 条数组：%v", got["video"])
	}

	// 空轨道：必须是 []（调用方不必判 null）。
	empty, err := RenderInfoJSON(InfoPayload{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var em map[string]interface{}
	if err := json.Unmarshal([]byte(empty), &em); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"video", "audio"} {
		if arr, ok := em[k].([]interface{}); !ok || len(arr) != 0 {
			t.Errorf("空 %s 必须是 []，实际 %v", k, em[k])
		}
	}
	if _, has := em["clips"]; has {
		t.Error("clips 为空时应当省略（omitempty）")
	}
}
