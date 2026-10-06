package util

import "strings"

// ---- 终端宽度治理的公共宽度工具（t64，第二步：标题折行 + 事件行截断）----
//
// 宽度口径与 internal/download.DisplayWidth 一致（ASCII 1 列、CJK/全角/emoji 2 列、
// 控制字符与组合记号 0 列）。**不能复用那个函数**：util 是 download 的下游依赖
// （download import util），反向引用会成环——所以这里按同一套区间表实现一份，
// 并与 http.go 的 screenClamp 共用同一个 runeDisplayWidth（一份表，两处使用）。

// runeDisplayWidth 返回单个字符占的显示列数。
func runeDisplayWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7F && r < 0xA0):
		return 0 // 控制字符不占列
	case r >= 0x1100 && r <= 0x115F, // 韩文字母
		r >= 0x2E80 && r <= 0x303E, // CJK 部首/标点
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF, // CJK 统一表意
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3, // 韩文音节
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // 全角
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1FAFF, // emoji
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	default:
		return 1
	}
}

// DisplayWidth 返回字符串的显示列数（组合记号/零宽/控制字符算 0 列）。
func DisplayWidth(s string) int {
	total := 0
	for _, r := range s {
		if r == 0x200B || r == 0x200C || r == 0x200D || r == 0xFEFF ||
			(r >= 0x0300 && r <= 0x036F) || (r >= 0x1AB0 && r <= 0x1AFF) {
			continue // 零宽与组合记号不占列
		}
		total += runeDisplayWidth(r)
	}
	return total
}

// TruncateDisplay 按显示宽度截断：放得下就原样返回；超宽时保留前 cols-1 列 + 「…」。
// cols<=0 返回空串；cols==1 只剩「…」（与屏幕版 screenClamp 同一取舍）。
func TruncateDisplay(s string, cols int) string {
	if cols <= 0 || s == "" {
		return ""
	}
	if DisplayWidth(s) <= cols {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeDisplayWidth(r)
		if used+w > cols-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// WrapDisplay 按显示宽度折行：只在整字符边界断开，**不丢字**（拼回去等于原文）。
// 单个字符本身超宽时允许该行变宽（与 download.PadDisplay 同一取舍：不丢信息）。
// cols<=0 或空串返回 nil。
func WrapDisplay(s string, cols int) []string {
	if s == "" || cols <= 0 {
		return nil
	}
	var out []string
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeDisplayWidth(r)
		if used > 0 && used+w > cols {
			out = append(out, b.String())
			b.Reset()
			used = 0
		}
		b.WriteRune(r)
		used += w
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
