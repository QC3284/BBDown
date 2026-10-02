package download

import (
	"strings"
	"testing"
)

// TestProgressFrameCursorAndResume 钉住进度行新增的两段（BBDownT 口径）：
//
//		P1/8 [#####---] | 33.01% 6.0 MB/s ETA 00:00:20 81.5/246.9 M 已续传 40.00 MB
//
//	  - **分P游标** `P1/8 ` 顶在整行最前面，只在多P任务（cursor 非空）出现；
//	  - **续传提示** `已续传 X` 在信息段末尾，只在有 base（resumeBase>0，即这次是断点续传）出现。
//
// 单P / 无续传时这两段一个字符都不出现——旧形态（2.12.8 的行）逐字不变，见第 ③ 条。
//
// 宽度：游标占的列数必须计入预算（progressFrame.prefixWidth），否则窄终端上会撑出终端；
// 宽度不足时按优先级裁剪，续传提示**最先**被裁（它是一次性信息，不如百分比/速率重要）。
//
// 变异验证：把游标段改成无条件渲染（单P 也带 P1/1）→ 第 ③ 条红；
// 把续传提示挪出 resumeBase 判断 → 第 ③ 条红；不把 prefixWidth 计入预算 → 40 列那条红。
func TestProgressFrameCursorAndResume(t *testing.T) {
	const mib = 1 << 20
	cursor := "P1/8"
	full := progressFrame{downloaded: 81*mib + mib/2, total: 246*mib + 9*mib/10, speedBps: 6 * mib, cursor: cursor, resumeBase: 40 * mib}

	// ① 多P + 续传：两段都在，且游标在最前面。
	frame := renderProgressFrameAt(120, full, '|')
	if got := strings.TrimLeft(frame, " "); !strings.HasPrefix(got, cursor+" [") {
		t.Errorf("分P游标没有顶在行首：%q", frame)
	}
	if !strings.Contains(frame, "已续传 40.00 MB") {
		t.Errorf("有 base 时缺少续传提示：%q", frame)
	}
	if !strings.Contains(frame, "33.01%") {
		t.Errorf("续传提示不该顶掉原有的百分比：%q", frame)
	}

	// ② 多P、全新下载（无 base）：只有游标段。
	multi := renderProgressFrameAt(120, progressFrame{downloaded: mib, total: 10 * mib, speedBps: mib, cursor: cursor}, '|')
	if !strings.HasPrefix(strings.TrimLeft(multi, " "), cursor+" [") || strings.Contains(multi, "已续传") {
		t.Errorf("无续传的多P行 = %q：应当只有游标段", multi)
	}

	// ③ 单P + 无续传：与旧口径逐字一致（两段都不出现）。
	plain := renderProgressFrameAt(120, progressFrame{downloaded: mib, total: 10 * mib, speedBps: mib}, '|')
	if strings.Contains(plain, "P1/") || strings.Contains(plain, "已续传") {
		t.Errorf("单P、无续传时不该出现游标或续传段：%q", plain)
	}
	if legacy := renderProgressFrameAt(120, progressFrame{downloaded: mib, total: 10 * mib, speedBps: mib}, '|'); legacy != plain {
		t.Errorf("同一输入的两次渲染不一致：%q vs %q", legacy, plain)
	}

	// ④ 单P 续传（有 base）：只有续传段（游标仍不出现）。
	resumed := renderProgressFrameAt(160, progressFrame{downloaded: 40 * mib, total: 246 * mib, speedBps: 6 * mib, resumeBase: 40 * mib}, '|')
	if strings.Contains(resumed, "P1/") || !strings.Contains(resumed, "已续传 40.00 MB") {
		t.Errorf("单P 续传行 = %q：应当只有续传段", resumed)
	}

	// ⑤ 任何宽度下整行都不超终端（游标与续传都算进预算）。
	for _, width := range []int{40, 60, 80, 120} {
		if got := DisplayWidth(renderProgressFrameAt(width, full, '|')); got > width {
			t.Errorf("宽度 %d 下渲染出 %d 列的行：%q", width, got, renderProgressFrameAt(width, full, '|'))
		}
	}
	// 窄终端优先裁掉续传提示，游标与百分比必须还在（选流/盯进度靠它们）。
	narrow := renderProgressFrameAt(60, full, '|')
	if strings.Contains(narrow, "已续传") {
		t.Errorf("60 列下续传提示没有被优先裁掉（它是低优先级信息）：%q", narrow)
	}
	if !strings.HasPrefix(strings.TrimLeft(narrow, " "), cursor+" [") || !strings.Contains(narrow, "33.01%") {
		t.Errorf("裁掉续传提示时不该连游标/百分比一起丢：%q", narrow)
	}
}
