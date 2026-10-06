package substore

import (
	"strings"
	"testing"
	"unicode"
)

// ---- t47 ① --per-sub-dir 的目录规划 ----

// TestSubDirNamePrecedenceAndSanitize 钉住目录名的取值顺序与净化：
// --name 显示名优先、缺省 target；分隔符/控制字符/单个点/两个点 一律净化
// （净化结果必须能当单层目录名用：不含路径分隔符与控制字符、非空、不是点目录）。
func TestSubDirNamePrecedenceAndSanitize(t *testing.T) {
	exact := []struct {
		name string
		sub  Subscription
		want string
	}{
		{"显示名优先", Subscription{Target: "mid:123", Name: "某UP"}, "某UP"},
		{"缺省用 target", Subscription{Target: "mid:123"}, "mid:123"},
		{"显示名只有空白算缺省", Subscription{Target: "mid:123", Name: "   "}, "mid:123"},
		{"单点被净化", Subscription{Target: "mid:1", Name: "."}, "_"},
		{"双点被净化", Subscription{Target: "..", Name: ".."}, "_"},
	}
	for _, c := range exact {
		if got := SubDirName(c.sub); got != c.want {
			t.Errorf("%s: SubDirName(%+v) = %q, want %q", c.name, c.sub, got, c.want)
		}
	}

	// 危险输入只看性质：不含分隔符/控制字符、非空、不是点目录（替换成哪个字符是实现细节）。
	dirty := []Subscription{
		{Target: "mid:1", Name: "a/b"},
		{Target: "mid:2", Name: "x\\y"},
		{Target: "mid:3", Name: "a\x01b"},
		{Target: "mid:4", Name: "  ..  "},
	}
	for _, sub := range dirty {
		got := SubDirName(sub)
		if got == "" || got == "." || got == ".." {
			t.Errorf("净化结果不能是空/点目录：输入 %q 得到 %q", sub.Name, got)
		}
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("净化结果不能含路径分隔符：输入 %q 得到 %q", sub.Name, got)
		}
		for _, r := range got {
			if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
				t.Errorf("净化结果不能含控制字符：输入 %q 得到 %q", sub.Name, got)
			}
		}
	}
}

// TestPlanSubDirsDedupesSameName 钉住重名序号：第一条不带序号，后面按订阅顺序加 -2/-3；
// 并且会绕开真名就叫 x-2 的目录（否则两条订阅仍然会撞在一起）。
func TestPlanSubDirsDedupesSameName(t *testing.T) {
	subs := []Subscription{
		{Target: "mid:1", Name: "同一个UP"},
		{Target: "mid:2", Name: "同一个UP"},
		{Target: "mid:3", Name: "同一个UP"},
		{Target: "mid:4", Name: "独一份"},
	}
	plan := PlanSubDirs(subs)
	want := map[string]string{
		"mid:1": "同一个UP",
		"mid:2": "同一个UP-2",
		"mid:3": "同一个UP-3",
		"mid:4": "独一份",
	}
	for target, name := range want {
		if got := plan[target]; got != name {
			t.Errorf("PlanSubDirs[%s] = %q, want %q（完整：%v）", target, got, name, plan)
		}
	}
	if len(plan) != len(want) {
		t.Errorf("规划结果条数 = %d, want %d", len(plan), len(want))
	}

	collide := PlanSubDirs([]Subscription{
		{Target: "t1", Name: "x"},
		{Target: "t2", Name: "x"},
		{Target: "t3", Name: "x-2"},
	})
	if collide["t1"] != "x" || collide["t2"] != "x-2" || collide["t3"] != "x-2-2" {
		t.Errorf("与真名 x-2 撞车时应当继续让开，实际 %v", collide)
	}

	dup := PlanSubDirs([]Subscription{{Target: "t", Name: "n"}, {Target: "t", Name: "别的"}})
	if len(dup) != 1 || dup["t"] != "n" {
		t.Errorf("同一 target 应当只规划一次并保留先出现的名字，实际 %v", dup)
	}
	if got := PlanSubDirs(nil); len(got) != 0 {
		t.Errorf("空输入应当返回空表，实际 %v", got)
	}
}
