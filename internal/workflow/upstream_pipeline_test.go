package workflow

import "testing"

// 本文件搬上游 DownloadPipelineTests 里的 ClampRoleAudioIndex 表：
// 每个 role 的音频列表与主音轨列表独立，用户选的序号可能越界——越界钳到末位、空列表跳过。

func TestUpstreamClampRoleAudioIndex(t *testing.T) {
	cases := []struct {
		aIndex, count, want int
	}{
		{5, 3, 2},  // 越界 → 钳到末位
		{-1, 3, 0}, // 负数 → 取首位
		{3, 1, 0},  // 只有一条 → 0
		{0, 0, -1}, // 无音频 → 标记跳过
	}
	for _, c := range cases {
		if got := clampRoleAudioIndex(c.aIndex, c.count); got != c.want {
			t.Errorf("clampRoleAudioIndex(%d, %d) = %d，上游期望 %d", c.aIndex, c.count, got, c.want)
		}
	}
}
