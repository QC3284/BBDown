package testsupport

import (
	"os"
	"path/filepath"
	"strings"
)

// TB 是 VerifyWiring 需要的 testing 能力（*testing.T 天然满足）。
//
// 为什么不直接 import testing：本包是非 _test 包，只被各包的 _test.go 引用；不 import testing
// 就不会把 testing 的 flag 注册链进任何产物，也不需要为一个接口付出真实的测试依赖。
type TB interface {
	Helper()
	TempDir() string
	Cleanup(func())
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
}

// VerifyWiring 是各包**薄接线用例**的共享主体：证明本包真的挂上了共享守卫，且本包的
// 文件名规约被共享判据认下。各包只需一行：
//
//	func TestWorkdirGuardWiring(t *testing.T) { testsupport.VerifyWiring(t, guardConfig()) }
//
// 判据（都是行为，四条都走共享实现）：
//  1. RunMain 在 m.Run 之前记录了起点，且它与当前 cwd 是同一个目录——守卫真的武装在整包上；
//  2. 本包规约认下自己的状态文件（ProbeNames，含原子写中途名），且不误报无关文件；
//  3. 共享判据真的会判红：在临时目录里造出状态文件 → Problems 非空，报红文本含文件名、目录
//     与本包的修复建议；原地改写（只动 mtime）同样判红；
//  4. 共享的还原分支真的在跑：切走 cwd → InspectAfterRun 还原并报告 Stray。
//
// 变异验证：把 Problems 改成恒返回 nil、去掉原地改写分支、或删掉 InspectAfterRun 的 os.Chdir
// 还原 → 本函数红；因为 cli 与 server 的接线用例都调用它，两侧会**同时**红（它们钉的是同一份
// 共享判据，这正是抽包的意义）。
func VerifyWiring(t TB, cfg Config) {
	t.Helper()

	// ① 守卫武装：起点已记录且与当前目录是同一个目录。
	if WorkdirAtStart() == "" {
		t.Fatal("RunMain 没有记录起始工作目录：包级守卫没有武装（TestMain 没接上？）")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取不到当前工作目录: %v", err)
	}
	if !SameDir(wd, WorkdirAtStart()) {
		t.Fatalf("当前 cwd %q 与 RunMain 记录的 %q 不是同一个目录：守卫的起点不可信", wd, WorkdirAtStart())
	}

	// ② 本包规约：ProbeNames 必须都被认下，无关文件一个都不许进。
	dir := t.TempDir()
	for _, probe := range cfg.ProbeNames {
		if err := os.WriteFile(filepath.Join(dir, probe), []byte("x"), 0o644); err != nil {
			t.Fatalf("造探针文件 %s 失败: %v", probe, err)
		}
	}
	for _, noise := range []string{"coverage.out", "other.json", "bbdown-guard-unrelated"} {
		if err := os.WriteFile(filepath.Join(dir, noise), []byte("x"), 0o644); err != nil {
			t.Fatalf("造无关文件 %s 失败: %v", noise, err)
		}
	}
	names := cfg.StateNames(dir)
	for _, probe := range cfg.ProbeNames {
		if !contains(names, probe) {
			t.Errorf("本包规约没有认下状态文件 %q（认出的是 %v）：守卫会瞎掉", probe, names)
		}
	}
	for _, noise := range []string{"coverage.out", "other.json", "bbdown-guard-unrelated"} {
		if contains(names, noise) {
			t.Errorf("无关文件 %q 被判成状态文件（认出的是 %v）：守卫会误报", noise, names)
		}
	}

	// ③ 共享判据真的判红：先取「干净」基线，再让探针出现。
	clean := t.TempDir()
	before := Snapshot(clean, cfg.StateNames(clean))
	if len(before) != 0 {
		t.Fatalf("干净目录不该有状态文件，实际 %v", before)
	}
	created := Snapshot(dir, cfg.StateNames(dir))
	problems := Problems(dir, before, created, cfg)
	if len(problems) == 0 {
		t.Fatal("状态文件出现在工作目录时必须判红（共享判据失效？）")
	}
	joined := strings.Join(problems, "\n")
	for _, want := range append(append([]string{dir}, cfg.ProbeNames...), cfg.InjectHint) {
		if want != "" && !strings.Contains(joined, want) {
			t.Errorf("报红文本要包含 %q，实际：%s", want, joined)
		}
	}

	// 内容不变、只刷新 mtime（真实路径上 rewriter 会这么做）同样判红。
	probePath := filepath.Join(dir, cfg.ProbeNames[0])
	futureTime := nowPlusHour()
	if err := os.Chtimes(probePath, futureTime, futureTime); err != nil {
		t.Fatalf("刷新 mtime 失败: %v", err)
	}
	if len(Problems(dir, created, Snapshot(dir, cfg.StateNames(dir)), cfg)) == 0 {
		t.Fatal("状态文件被原地改写（内容不变、mtime 变化）时必须判红")
	}

	// ④ 共享的还原分支真的在跑：切走 cwd → 收尾检查把它扳回来并报告 Stray。
	base := t.TempDir()
	start := filepath.Join(base, "pkg")
	stray := filepath.Join(base, "stray")
	for _, d := range []string{start, stray} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("建目录 %s 失败: %v", d, err)
		}
	}
	restore, err := Chdir(stray)
	if err != nil {
		t.Fatalf("切到 %s 失败: %v", stray, err)
	}
	t.Cleanup(restore)
	rep := InspectAfterRun(start, Snapshot(start, cfg.StateNames(start)), cfg)
	if len(rep.Problems) != 0 {
		t.Fatalf("原目录还在、能还原，就不该判红，实际：%v", rep.Problems)
	}
	if !SameDir(rep.Stray, stray) {
		t.Fatalf("报告要列出被纠正的用例目录：Stray = %q，want %q", rep.Stray, stray)
	}
	if now, err := os.Getwd(); err != nil || !SameDir(now, start) {
		t.Fatalf("cwd 应当被还原到 %s，实际 %q（err=%v）", start, now, err)
	}
}

// contains 是「文件名在不在规约结果里」的小工具（切片很短，线性扫足够）。
func contains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
