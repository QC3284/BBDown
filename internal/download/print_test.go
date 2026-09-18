package download

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// TestPrintAllTracksMatchesUpstream 钉住清单的三段顺序与 --only-show-info 的取流地址：
// 上游 Display.cs 先打印背景音频流与配音（两者都存在时），再视频流、音频流；
// 只有视频流与音频流在 onlyShowInfo 时各跟一行 baseUrl。
func TestPrintAllTracksMatchesUpstream(t *testing.T) {
	result := &entity.ParsedResult{
		BackgroundAudioTracks: []entity.Audio{{Codecs: "ec-3", Bandwidth: 448, Dur: 100}},
		RoleAudioList: []entity.AudioMaterialInfo{{
			Title: "中文",
			Audio: []entity.Audio{
				{Codecs: "mp4a.40.2", Bandwidth: 128, Dur: 100},
				{Codecs: "mp4a.40.2", Bandwidth: 64, Dur: 100},
			},
		}},
		VideoTracks: []entity.Video{{Dfn: "1080P", Res: "1920x1080", Codecs: "avc1", FPS: "30", Bandwidth: 1000, Dur: 100, BaseURL: "https://cdn/v.m4s"}},
		AudioTracks: []entity.Audio{{Codecs: "mp4a.40.2", Bandwidth: 128, Dur: 100, BaseURL: "https://cdn/a.m4s"}},
	}

	out := captureStdout(t, func() { PrintAllTracks(result, 100, false) })
	for _, want := range []string{
		"共计1条背景音频流.",
		"共计1条配音, 每条包含2条配音流.",
		"共计1条视频流.",
		"共计1条音频流.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("清单缺少 %q，实际输出 %q", want, out)
		}
	}
	// 背景音频/配音列表在视频列表之前（上游顺序）
	if strings.Index(out, "条背景音频流") > strings.Index(out, "条视频流") {
		t.Errorf("背景音频应在视频流之前打印：%q", out)
	}
	// 默认不打印取流地址
	if strings.Contains(out, "https://cdn/") {
		t.Errorf("未开 --only-show-info 不应打印 baseUrl：%q", out)
	}

	out = captureStdout(t, func() { PrintAllTracks(result, 100, true) })
	if !strings.Contains(out, "https://cdn/v.m4s") || !strings.Contains(out, "https://cdn/a.m4s") {
		t.Errorf("--only-show-info 必须给出视频/音频的 baseUrl，实际输出 %q", out)
	}
	// 背景音频/配音不跟地址（上游只在视频/音频两段里打印）
	if got := strings.Count(out, "https://cdn/"); got != 2 {
		t.Errorf("baseUrl 出现 %d 次，期望 2 次（仅视频与音频各一次）：%q", got, out)
	}
}
