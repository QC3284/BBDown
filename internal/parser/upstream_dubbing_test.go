package parser

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// TestExtractDubbingInfo 钉住 dubbing_info 的解析（上游 Parser.cs 同一段）：
// 此前本仓完全没读这段，工作流的背景音轨/配音下载循环因此是死代码。

func TestExtractDubbingInfo(t *testing.T) {
	data := map[string]interface{}{
		"dubbing_info": map[string]interface{}{
			"background_audio": []interface{}{
				map[string]interface{}{
					"id": "30300",
					// 带端口的直连（PCDN）地址会被 baseURLRegex 命中，此时应改用 backup_url
					"base_url":   "https://x.mcdn.bilivideo.cn:8082/bg.m4s",
					"backup_url": []interface{}{"https://upos-sz-mirrorcoso1.bilivideo.com/bg.m4s"},
					"bandwidth":  float64(448000),
					"codecs":     "ec-3",
				},
			},
			"role_audio_list": []interface{}{
				map[string]interface{}{
					"audio_id":    "../逃逸",
					"title":       "中文配音",
					"person_name": "张三",
					"audio": []interface{}{
						map[string]interface{}{"id": "30280", "base_url": "https://x/a.m4s", "bandwidth": float64(128000), "codecs": "mp4a.40.2"},
						map[string]interface{}{"id": "30232", "base_url": "https://x/b.m4s", "bandwidth": float64(64000), "codecs": "mp4a.40.2"},
					},
				},
			},
		},
	}

	var result entity.ParsedResult
	extractDubbingInfo(data, &result, "170001", "2", 100)

	if len(result.BackgroundAudioTracks) != 1 {
		t.Fatalf("背景音轨应解析出 1 条，实际 %d", len(result.BackgroundAudioTracks))
	}
	bg := result.BackgroundAudioTracks[0]
	if bg.ID != "30300" || bg.Dfn != "30300" {
		t.Errorf("背景音轨 id/dfn 不对: %+v", bg)
	}
	if bg.Bandwidth != 448 {
		t.Errorf("带宽应按 kbps 取整（上游 /1000），实际 %d", bg.Bandwidth)
	}
	if bg.Dur != 100 {
		t.Errorf("时长应取页时长，实际 %d", bg.Dur)
	}
	if !strings.Contains(bg.BaseURL, "upos-sz-mirrorcoso1") {
		t.Errorf("base_url 匹配 host 正则时应改用 backup_url，实际 %q", bg.BaseURL)
	}

	if len(result.RoleAudioList) != 1 {
		t.Fatalf("配音应解析出 1 个 role，实际 %d", len(result.RoleAudioList))
	}
	role := result.RoleAudioList[0]
	if role.Title != "中文配音" || role.PersonName != "张三" {
		t.Errorf("配音 role 的名称不对: %+v", role)
	}
	if len(role.Audio) != 2 {
		t.Errorf("配音音轨数 = %d，期望 2", len(role.Audio))
	}
	if role.Path == "" {
		t.Error("配音产物路径必须被计算出来")
	}
	if strings.Contains(role.Path, "..") {
		t.Errorf("audio_id 未净化，路径穿越未被拦住: %q", role.Path)
	}
	if !strings.HasPrefix(role.Path, "170001") || !strings.HasSuffix(role.Path, ".m4a") {
		t.Errorf("路径形状应为 <aid>/<aid>.<cid>.<seg>.m4a，实际 %q", role.Path)
	}

	// 没有 dubbing_info 时不得panic，也不应产出任何轨道
	var empty entity.ParsedResult
	extractDubbingInfo(map[string]interface{}{}, &empty, "1", "2", 10)
	if len(empty.BackgroundAudioTracks) != 0 || len(empty.RoleAudioList) != 0 {
		t.Errorf("无 dubbing_info 不应产出轨道")
	}
}

// TestShouldExtractDubbing 钉住门控：只有 APP API + 番剧才读 dubbing_info（上游同一条件）。
func TestShouldExtractDubbing(t *testing.T) {
	cases := []struct {
		appAPI bool
		aidOri string
		want   bool
	}{
		{true, "ep:12345", true},
		{true, "cheese:123", true},
		{true, "170001", false},    // APP 但 UGC：没有 dubbing_info
		{false, "ep:12345", false}, // 番剧但非 APP：部分接口不返回该节点
		{false, "170001", false},
	}
	for _, c := range cases {
		if got := shouldExtractDubbing(c.appAPI, c.aidOri); got != c.want {
			t.Errorf("shouldExtractDubbing(%v, %q) = %v，期望 %v", c.appAPI, c.aidOri, got, c.want)
		}
	}
}
