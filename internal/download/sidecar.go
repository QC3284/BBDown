package download

import "strings"

// 侧车（字幕 / 弹幕 / 封面）的状态行：BBDownT 口径，打在主进度行下面一行：
//
//	侧车: 字幕 ✔ · 弹幕 ✔ · 封面 ✔
//
// 三种状态：
//   - **未启用**（用户没开字幕/弹幕/封面）：该项**不出现**（不是打 ✗——它与本次下载无关，
//     用户没要的东西不该占一行）；
//   - 已启用、还没完成：`字幕 …`（给用户一个「在等它」的信号）；
//   - 已完成：`字幕 ✔`。
//
// 顺序固定（见 sidecarOrder）：同一批产物在不同运行里的行必须长得一样，用户与脚本都靠它对比。
// 全部未启用时 Line() 返回空串——调用方不打这一行（明细里不该出现一行空壳）。
//
// 为什么放在 internal/download：这一行与进度帧同属「下载过程的终端输出」，共用同一套
// 显示宽度工具；编排层（workflow）只负责在产物落盘时 MarkDone 并重画。
const (
	sidecarDone    = "✔"
	sidecarPending = "…"
	sidecarPrefix  = "侧车: "
	sidecarSep     = " · "
)

// sidecarOrder 是侧车项的固定显示顺序。
var sidecarOrder = []string{"字幕", "弹幕", "封面"}

// SidecarStatus 是本次下载的侧车状态集合（只含**启用的**项）。
type SidecarStatus struct {
	enabled []string
	done    map[string]bool
}

// NewSidecarStatus 按 sidecarOrder 的固定顺序注册启用的侧车项（未启用的不要传进来）。
// 忽略不认识的标签：调用方多传一个名字不该让状态行冒出野生项。
func NewSidecarStatus(enabled ...string) *SidecarStatus {
	set := make(map[string]bool, len(enabled))
	for _, label := range enabled {
		set[label] = true
	}
	s := &SidecarStatus{done: make(map[string]bool, len(enabled))}
	for _, label := range sidecarOrder {
		if set[label] {
			s.enabled = append(s.enabled, label)
		}
	}
	return s
}

// MarkDone 记一项侧车完成（不认识的标签忽略）。
func (s *SidecarStatus) MarkDone(label string) {
	if s == nil {
		return
	}
	for _, known := range s.enabled {
		if known == label {
			s.done[label] = true
			return
		}
	}
}

// Line 渲染整行；没有任何启用的侧车时返回空串（调用方据此不打这一行）。
func (s *SidecarStatus) Line() string {
	if s == nil || len(s.enabled) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.enabled))
	for _, label := range s.enabled {
		parts = append(parts, label+" "+sidecarStatusMark(s.done[label]))
	}
	return sidecarPrefix + strings.Join(parts, sidecarSep)
}

// sidecarStatusMark 是单项的状态记号：完成 ✔、未完成 …。
func sidecarStatusMark(done bool) string {
	if done {
		return sidecarDone
	}
	return sidecarPending
}
