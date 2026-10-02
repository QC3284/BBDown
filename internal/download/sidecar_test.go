package download

import (
	"strings"
	"testing"
)

// TestSidecarStatusLine 钉住侧车状态行的 BBDownT 形态与三态语义：
// 完成 ✔、未完成 …、**未启用不出现**（用户没要的东西不占一行）；全部未启用时整行不打。
//
// 变异验证：把「未启用不出现」改成「一律输出三个标签」→ 第 3/4 条红；
// 把 ✔ 换成别的记号 → 第 1 条红。
func TestSidecarStatusLine(t *testing.T) {
	// ① 三项全启用、全部完成：逐字形态（交付说明里的目标行）。
	all := NewSidecarStatus("字幕", "弹幕", "封面")
	if got, want := all.Line(), "侧车: 字幕 … · 弹幕 … · 封面 …"; got != want {
		t.Errorf("未完成时的状态行 = %q，期望 %q", got, want)
	}
	all.MarkDone("字幕")
	all.MarkDone("弹幕")
	all.MarkDone("封面")
	if got, want := all.Line(), "侧车: 字幕 ✔ · 弹幕 ✔ · 封面 ✔"; got != want {
		t.Errorf("全部完成后 = %q，期望 %q", got, want)
	}

	// ② 部分完成：各自的状态独立（弹幕还没好，字幕已经好了）。
	part := NewSidecarStatus("字幕", "弹幕")
	part.MarkDone("字幕")
	if got, want := part.Line(), "侧车: 字幕 ✔ · 弹幕 …"; got != want {
		t.Errorf("部分完成 = %q，期望 %q", got, want)
	}

	// ③ 未启用的项**不出现**（既不是 ✗ 也不是空位）。
	only := NewSidecarStatus("封面")
	only.MarkDone("封面")
	if got, want := only.Line(), "侧车: 封面 ✔"; got != want {
		t.Errorf("只开封面 = %q，期望 %q", got, want)
	}
	if strings.Contains(only.Line(), "字幕") || strings.Contains(only.Line(), "弹幕") {
		t.Errorf("未启用的侧车出现在了状态行里：%q", only.Line())
	}

	// ④ 一个都没启用：整行不打（返回空串，调用方据此跳过）。
	if got := NewSidecarStatus().Line(); got != "" {
		t.Errorf("没有启用任何侧车时状态行 = %q，期望空串", got)
	}

	// ⑤ 顺序固定（与注册顺序无关），未知标签被忽略。
	unordered := NewSidecarStatus("封面", "字幕", "不存在的项")
	unordered.MarkDone("字幕")
	if got, want := unordered.Line(), "侧车: 字幕 ✔ · 封面 …"; got != want {
		t.Errorf("注册顺序不应影响显示顺序：got %q, want %q", got, want)
	}
	unordered.MarkDone("不存在的项") // 不该 panic，也不该改变行
	if got, want := unordered.Line(), "侧车: 字幕 ✔ · 封面 …"; got != want {
		t.Errorf("未知标签影响了状态行：got %q, want %q", got, want)
	}
	var nilStatus *SidecarStatus
	if got := nilStatus.Line(); got != "" {
		t.Errorf("nil 状态行 = %q，期望空串", got)
	}
	nilStatus.MarkDone("字幕") // 不该 panic
}
