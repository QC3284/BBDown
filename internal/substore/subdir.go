package substore

import (
	"fmt"
	"strings"

	"github.com/QC3284/BBDown-go/internal/util"
)

// ---- --per-sub-dir 的目录规划（t47 ①，吸收上游 1.6.21）----
//
// 默认关闭：不传 --per-sub-dir 时所有订阅共用 work-dir，产物平铺（与 2.15.2 逐字一致）。
// 打开后每条订阅下到 <work-dir>/<订阅名>/，订阅名取 --name 显示名、缺省 target、经净化；
// 净化后重名的按订阅顺序追加 -2/-3——否则两条订阅会互相覆盖产物（比不分子目录更糟）。

// SubDirName 是单条订阅的目录名（未去重）：--name 显示名优先，缺省 target，
// 经 util.SanitizePathSegment 净化（同一层目录名：必须挡掉 / \ 控制字符与 . / ..）。
func SubDirName(sub Subscription) string {
	name := strings.TrimSpace(sub.Name)
	if name == "" {
		name = strings.TrimSpace(sub.Target)
	}
	dir := strings.TrimSpace(util.SanitizePathSegment(name))
	if dir == "" {
		dir = "sub"
	}
	return dir
}

// PlanSubDirs 给一批订阅规划目录名，返回 target → 目录名。
//
// 同一 target 只规划一次（调用方在进入调度前已按 target 去重，这里再兜一层）；
// 净化后**重名**的按订阅顺序追加 -2/-3 序号（第一条不带序号），并且会绕开已经用掉的
// 「真名就叫 x-2」的目录——否则两条订阅仍然会撞在一起。
func PlanSubDirs(subs []Subscription) map[string]string {
	out := make(map[string]string, len(subs))
	used := make(map[string]int, len(subs))
	for _, sub := range subs {
		if _, ok := out[sub.Target]; ok {
			continue
		}
		base := SubDirName(sub)
		name := base
		for {
			used[name]++
			if used[name] == 1 {
				break
			}
			name = fmt.Sprintf("%s-%d", base, used[name])
		}
		out[sub.Target] = name
	}
	return out
}
