package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是共享守卫的**主用例**：判据（快照 / 比较 / SameDir / 还原 / RunMain）在这里被逐条钉住。
//
// 抽包前这些用例在 internal/cli 与 internal/server 各有一份镜像（判据断言 + 各自的文件名），
// 现在合并去重：判据只测一遍，两边不同的只是「文件名规约」，用两组 Config 覆盖——
//
//   - testConfig（server 形态）：状态文件本尊 + 裸 .tmp + 前缀匹配 .tmp-<jobid>；
//   - fixedOnlyConfig（cli 形态）：只有两个精确名，不带前缀（.tmp-xxx 不算它的状态文件）。
//
// 各包不再重复这些判据，只留一行接线用例（见各包的 workdir_guard_test.go）。

const testStateFile = "state.json"

// testConfig 是 server 形态的规约：精确名 + 前缀匹配的原子写中途文件。
func testConfig() Config {
	return Config{
		StateNames: func(dir string) []string {
			return StateNames(dir,
				[]string{testStateFile, testStateFile + ".tmp"},
				[]string{testStateFile + ".tmp-"})
		},
		ProbeNames:  []string{testStateFile, testStateFile + ".tmp-deadbeef"},
		InjectHint:  "请给被测代码注入临时目录，不要让测试依赖进程 cwd。",
		RewriteHint: "状态文件必须写进 t.TempDir()。",
	}
}

// fixedOnlyConfig 是 cli 形态的规约：只认两个精确名（不带前缀）。
func fixedOnlyConfig() Config {
	return Config{
		StateNames: func(dir string) []string {
			return StateNames(dir, []string{testStateFile, testStateFile + ".tmp"}, nil)
		},
		ProbeNames:  []string{testStateFile, testStateFile + ".tmp"},
		InjectHint:  "请给被测代码注入临时目录（如 cfg.WorkDir = t.TempDir()）。",
		RewriteHint: "状态文件必须写进 t.TempDir()。",
	}
}

// chdir 把进程 cwd 切到 dir，并保证本用例结束时切回进入时的目录。
// 不用 t.Chdir：本文件的用例要复现的正是「用例自己 os.Chdir 却不还原」这一场景，
// 守卫的兜底语义才有被测对象。
func chdir(t *testing.T, dir string) {
	t.Helper()
	restore, err := Chdir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
}

// TestStateNamesMatchesPolicyPrecisely 钉住「只盯本包的状态文件」这条精确性：
// 精确名与前缀名都要认下，无关文件（覆盖率/性能文件/同前缀的别的东西）一个都不许进。
//
// 变异验证：把前缀匹配改成 strings.Contains（而不是 HasPrefix）→ 「无关的中缀文件」那条红。
func TestStateNamesMatchesPolicyPrecisely(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		testStateFile, testStateFile + ".tmp", testStateFile + ".tmp-deadbeef",
		"coverage.out", testStateFile + ".bak", testStateFile + ".tmpx", "other.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, testStateFile+".tmp-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := testConfig().StateNames(dir)
	want := []string{testStateFile, testStateFile + ".tmp", testStateFile + ".tmp-deadbeef"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("带前缀的规约 = %v，期望 %v（只认本包状态文件，目录不算）", got, want)
	}
	// cli 形态：不带前缀 → .tmp-<id> 不是它的状态文件（这正是两包规约的差别）。
	got = fixedOnlyConfig().StateNames(dir)
	want = []string{testStateFile, testStateFile + ".tmp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("精确名规约 = %v，期望 %v", got, want)
	}

	// 目录读不了（不存在）时返回 nil，不 panic。
	if got := testConfig().StateNames(filepath.Join(dir, "missing")); got != nil {
		t.Fatalf("不存在的目录应返回 nil，实际 %v", got)
	}
}

// TestSnapshotTracksDigestAndModTime 钉住快照的两个字段：内容摘要与 mtime。
// 两个都要——重写一个内容不变的文件改的是 mtime，只比内容会漏掉「原地改写」。
func TestSnapshotTracksDigestAndModTime(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig()
	before := Snapshot(dir, cfg.StateNames(dir))
	if len(before) != 0 {
		t.Fatalf("干净目录不该有状态文件，实际 %v", before)
	}

	path := filepath.Join(dir, testStateFile)
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot(dir, cfg.StateNames(dir))
	first, ok := snap[testStateFile]
	if !ok || first.Digest == "" || first.ModTime == 0 {
		t.Fatalf("快照缺少内容摘要或 mtime：%+v", snap)
	}

	// 内容变了 → 摘要变。
	if err := os.WriteFile(path, []byte("v2-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Snapshot(dir, cfg.StateNames(dir))[testStateFile]; got.Digest == first.Digest {
		t.Fatalf("内容改写后摘要没变：%+v", got)
	}
}

// TestProblemsFlagsCreatedRewrittenAndTempResidue 是两包原有「状态文件判红」用例的合并版：
// 一组判据跑两种文件名规约，逐条覆盖四种形态 + 两条精确性反例。
//
//  1. 状态文件出现（本尊）；
//  2. 内容不变、只刷新 mtime（真实路径上 rewriter 会这么做）；
//  3. 原子写中途的 .tmp / .tmp-<id> 残留；
//  4. 反例：与状态文件无关的名字不得进快照、不得判红。
//
// 变异验证：把 Problems 改成恒返回 nil → 1/2/3 全红。
func TestProblemsFlagsCreatedRewrittenAndTempResidue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   Config
		resid string // 该规约下应该判红的「原子写中途名」
	}{
		{"带前缀的规约（server 形态）", testConfig(), testStateFile + ".tmp-deadbeef"},
		{"精确名规约（cli 形态）", fixedOnlyConfig(), testStateFile + ".tmp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			before := Snapshot(dir, tc.cfg.StateNames(dir))
			if len(before) != 0 {
				t.Fatalf("干净目录不该有状态文件，实际 %v", before)
			}

			// ① 出现 → 判红，且报红文本点出文件名、目录与本包修复建议。
			path := filepath.Join(dir, testStateFile)
			if err := os.WriteFile(path, []byte("[{\"url\":\"BV1xx411c7mD\"}]"), 0o644); err != nil {
				t.Fatal(err)
			}
			created := Snapshot(dir, tc.cfg.StateNames(dir))
			problems := Problems(dir, before, created, tc.cfg)
			if len(problems) == 0 {
				t.Fatal("状态文件出现在工作目录时必须判红")
			}
			joined := strings.Join(problems, "\n")
			for _, want := range []string{testStateFile, dir, tc.cfg.InjectHint} {
				if !strings.Contains(joined, want) {
					t.Fatalf("报红文本要包含 %q，实际：%s", want, joined)
				}
			}

			// ② 内容不变、只刷新 mtime → 同样判红（mtime 是快照的一半判据）。
			future := nowPlusHour()
			if err := os.Chtimes(path, future, future); err != nil {
				t.Fatal(err)
			}
			rewritten := Problems(dir, created, Snapshot(dir, tc.cfg.StateNames(dir)), tc.cfg)
			if len(rewritten) == 0 {
				t.Fatal("状态文件被原地改写（内容不变、mtime 变化）时必须判红")
			}
			if !strings.Contains(strings.Join(rewritten, "\n"), tc.cfg.RewriteHint) {
				t.Fatalf("改写分支要给出本包修复建议，实际：%v", rewritten)
			}

			// ③ 原子写中途名残留 → 判红（它会随仓库一起被提交/被 diff 看到）。
			if err := os.WriteFile(filepath.Join(dir, tc.resid), []byte("partial"), 0o644); err != nil {
				t.Fatal(err)
			}
			if len(Problems(dir, created, Snapshot(dir, tc.cfg.StateNames(dir)), tc.cfg)) == 0 {
				t.Fatalf("中途文件 %s 残留必须判红", tc.resid)
			}

			// ④ 精确性反例：无关文件不得进快照。
			for _, noise := range []string{"coverage.out", testStateFile + ".bak", "other.json"} {
				if err := os.WriteFile(filepath.Join(dir, noise), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			snap := Snapshot(dir, tc.cfg.StateNames(dir))
			for _, noise := range []string{"coverage.out", testStateFile + ".bak", "other.json"} {
				if _, ok := snap[noise]; ok {
					t.Errorf("无关文件 %q 被当成状态文件快照了：守卫会误报", noise)
				}
			}
			if _, ok := snap[testStateFile]; !ok {
				t.Fatalf("状态文件本尊必须仍在快照里：%v", snap)
			}
		})
	}
}

// TestSameDirAcceptsSpellings 钉住「同一目录的两种写法」：符号链接写法与解析后写法是同一个目录，
// 不同目录必须判否（防 helper 恒真）。这是 macOS /var → /private/var 的跨平台复现。
//
// Windows 默认不允许普通用户创建符号链接（需要开发者模式 / SeCreateSymbolicLinkPrivilege），
// 构造不出来就 t.Skip 并写明原因；Linux/macOS 上必须跑。
//
// 变异验证：把 SameDir 改回字符串比较 → 本用例红。
func TestSameDirAcceptsSpellings(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "pkg-real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "pkg-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本平台不允许创建符号链接（%v）：跳过「同一目录的不同写法」场景", err)
	}

	if !SameDir(link, real) {
		t.Fatalf("SameDir(%q, %q) = false，同一目录的两种写法必须判为相同", link, real)
	}
	if SameDir(real, filepath.Join(base, "elsewhere")) {
		t.Fatalf("SameDir(%q, %q) = true，不同目录必须判否", real, filepath.Join(base, "elsewhere"))
	}
	if SameDir("", real) || SameDir(real, "") {
		t.Fatal("空路径必须判否（两个空串不该被当成同一目录）")
	}
}

// TestInspectAfterRunRestoresStrayWorkdir：用例把 cwd 切到别处（目录还在）时，
// 守卫必须把它还原回来并通过，同时在报告里给出被纠正的目录。
//
// 变异验证：去掉 InspectAfterRun 里的 os.Chdir 还原 → 本用例红。
func TestInspectAfterRunRestoresStrayWorkdir(t *testing.T) {
	base := t.TempDir()
	pkg := filepath.Join(base, "pkg") // 模拟包目录（会话开始时的原目录）
	stray := filepath.Join(base, "stray")
	for _, d := range []string{pkg, stray} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	chdir(t, stray)

	cfg := testConfig()
	rep := InspectAfterRun(pkg, Snapshot(pkg, cfg.StateNames(pkg)), cfg)
	if len(rep.Problems) != 0 {
		t.Fatalf("原目录还在、能还原，就不该判红，实际：%v", rep.Problems)
	}
	if !SameDir(rep.Stray, stray) {
		t.Fatalf("报告要列出被纠正的用例目录：Stray = %q，want %q", rep.Stray, stray)
	}
	if now, err := os.Getwd(); err != nil || !SameDir(now, pkg) {
		t.Fatalf("cwd 应当被还原到 %s，实际 %q（err=%v）", pkg, now, err)
	}
}

// TestInspectAfterRunAcceptsSymlinkedSpelling：同一目录的两种写法不算偏离（stray 留空），
// 且「用符号链接写法还原」之后的复核同样走 SameDir——macOS CI 上判红的正是这一条。
func TestInspectAfterRunAcceptsSymlinkedSpelling(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "pkg-real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "pkg-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本平台不允许创建符号链接（%v）：跳过「同一目录的不同写法」场景", err)
	}
	cfg := testConfig()

	// ① cwd 经符号链接进入、start 记为解析后路径。
	chdir(t, link)
	rep := InspectAfterRun(real, Snapshot(real, cfg.StateNames(real)), cfg)
	if len(rep.Problems) != 0 || rep.Stray != "" {
		t.Fatalf("同一目录的两种写法不该判红或算偏离，实际 problems=%v stray=%q", rep.Problems, rep.Stray)
	}
	// ② 反向：start 用符号链接写法、Getwd 返回解析后路径——macOS /var → /private/var 的形态。
	rep = InspectAfterRun(link, Snapshot(link, cfg.StateNames(link)), cfg)
	if len(rep.Problems) != 0 || rep.Stray != "" {
		t.Fatalf("start 用符号链接写法、cwd 被解析，不该判红，实际 problems=%v stray=%q", rep.Problems, rep.Stray)
	}
	// ③ 真偏离 + 用符号链接写法还原：还原后的复核同样必须走 SameDir。
	stray := filepath.Join(base, "stray")
	if err := os.Mkdir(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, stray)
	rep = InspectAfterRun(link, Snapshot(link, cfg.StateNames(link)), cfg)
	if len(rep.Problems) != 0 {
		t.Fatalf("原目录还在、能还原，就不该判红，实际：%v", rep.Problems)
	}
	if !SameDir(rep.Stray, stray) {
		t.Fatalf("报告要列出被纠正的用例目录：Stray = %q，want %q", rep.Stray, stray)
	}
	if now, err := os.Getwd(); err != nil || !SameDir(now, real) {
		t.Fatalf("cwd 应当被还原到 %q，实际 %q（err=%v）", real, now, err)
	}
}

// TestInspectAfterRunFailsWhenStartDirGone：原目录已不存在时还原必然失败——此时必须判红，
// 且报红文本要给出原目录路径（可照做的修复建议）。
//
// 变异验证：去掉还原失败分支的判红 → 本用例红。
func TestInspectAfterRunFailsWhenStartDirGone(t *testing.T) {
	base := t.TempDir()
	gone := filepath.Join(base, "pkg") // 原目录，稍后删除；它不是 cwd，Windows 也删得掉
	stray := filepath.Join(base, "stray")
	for _, d := range []string{gone, stray} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	chdir(t, stray)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	rep := InspectAfterRun(gone, Snapshot(gone, cfg.StateNames(gone)), cfg)
	if len(rep.Problems) == 0 {
		t.Fatal("原目录已不存在、还原失败，必须判红")
	}
	if joined := strings.Join(rep.Problems, "\n"); !strings.Contains(joined, gone) {
		t.Fatalf("报红文本要给出原目录路径 %s，实际：%s", gone, joined)
	}
}

// TestInspectAfterRunRestoresFromDeletedWorkdir：跨平台复现 CI 上那条路径——
// os.Chdir(tmp) 之后 os.Remove(tmp)，cwd 指向一个已被删除的目录。
// 断言两条分支：原目录还在 → 还原并通过；原目录也没了 → 判红。
//
// Linux 的 getcwd(2) 此时返回 ENOENT（读不到 cwd），Windows/macOS 返回那个已消失的路径；
// 两条分支都不依赖具体返回值，而是落在「先 os.Chdir 还原」这一动作上。
func TestInspectAfterRunRestoresFromDeletedWorkdir(t *testing.T) {
	base := t.TempDir()
	pkg := filepath.Join(base, "pkg")
	if err := os.Mkdir(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, pkg)
	cfg := testConfig()

	// deletedWorkdir 建一个临时目录、切进去、再删掉它——构造「cwd 指向已删除目录」。
	deletedWorkdir := func() string {
		t.Helper()
		d, err := os.MkdirTemp("", "bbdown-guard-deleted-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(d); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(d); err != nil {
			// Windows 不允许删除进程当前目录，构造不出这一情形（CI 上该平台也因此才红）。
			t.Skipf("本平台不允许删除进程当前目录（%v）：跳过「cwd 指向已删除目录」场景", err)
		}
		return d
	}

	// ① 能还原 → 通过：原目录还在，守卫必须先把它切回去。
	deletedWorkdir()
	rep := InspectAfterRun(pkg, Snapshot(pkg, cfg.StateNames(pkg)), cfg)
	if len(rep.Problems) != 0 {
		t.Fatalf("原目录还在，还原后应当通过，实际：%v", rep.Problems)
	}
	if rep.Stray == "" {
		t.Fatal("cwd 曾被切走，报告里必须出现被纠正的目录（哪怕读不到路径）")
	}
	if now, err := os.Getwd(); err != nil || !SameDir(now, pkg) {
		t.Fatalf("cwd 应当被还原到 %s，实际 %q（err=%v）", pkg, now, err)
	}

	// ② 无法还原 → 判红：原目录也被删掉，还原无路可走。
	gone := filepath.Join(base, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	deletedWorkdir()
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	rep = InspectAfterRun(gone, Snapshot(gone, cfg.StateNames(gone)), cfg)
	if len(rep.Problems) == 0 {
		t.Fatal("原目录已删除、还原失败，必须判红")
	}
	if joined := strings.Join(rep.Problems, "\n"); !strings.Contains(joined, gone) {
		t.Fatalf("报红文本要给出原目录路径 %s，实际：%s", gone, joined)
	}
}

// TestRunMainRecordsStartAndReturnsRunCode 钉住共享 TestMain 主体（RunMain）的两件事：
// 起点被记录（守卫才算武装），以及退出码语义——run 的码原样传回，但**起点目录里出现状态文件时
// 会被抬成非 0**（否则 CI 会绿着漏过写进仓库的状态文件）。
//
// 「出现状态文件」用 run 回调在起点目录里现造一个探针文件来复现：RunMain 的 before 快照在 run
// **之前**取，所以只有 run 期间写进去的东西才会被判红——这正是真实测试运行污染仓库的形态。
// 探针文件随用例清理删除（名字也带 bbdown-guard-probe 前缀，一眼看得出是测试产物）。
//
// 变异验证：删掉 workdirStart 赋值 → 本用例红（各包的接线用例也会红）；
// 删掉「有污染时抬高退出码」那三行 → 第二段红。
func TestRunMainRecordsStartAndReturnsRunCode(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// ① run 的退出码原样传回（起点干净时收尾检查不改动它）。
	if code := RunMain(func() int { return 7 }, selfGuardConfig()); code != 7 {
		t.Fatalf("RunMain 返回 %d，期望 run 的退出码 7（无污染时不该改退出码）", code)
	}
	// ② 起点被记录，且与当前 cwd 是同一个目录。
	if WorkdirAtStart() == "" {
		t.Fatal("RunMain 没有记录起始工作目录：包级守卫没有武装")
	}
	if !SameDir(wd, WorkdirAtStart()) {
		t.Fatalf("记录的起点 %q 与当前 cwd %q 不是同一个目录", WorkdirAtStart(), wd)
	}

	// ③ 起点目录里在「运行期间」出现状态文件 → 退出码被抬成非 0。
	const probe = "bbdown-guard-probe.state"
	t.Cleanup(func() { _ = os.Remove(filepath.Join(wd, probe)) })
	cfg := selfGuardConfig()
	cfg.StateNames = func(string) []string { return []string{probe} }
	cfg.InjectHint = "把状态文件写进 t.TempDir()"
	code := RunMain(func() int {
		if err := os.WriteFile(filepath.Join(wd, probe), []byte("x"), 0o644); err != nil {
			t.Errorf("造探针文件失败: %v", err)
		}
		return 0
	}, cfg)
	if code == 0 {
		t.Fatal("起点目录里出现状态文件时，RunMain 必须把退出码抬成非 0")
	}
}

// TestVerifyWiringIsTheSharedWiringCheck 在共享包里跑一遍接线检查：证明各包那一行确实有效。
// （各包的 workdir_guard_test.go 各自再调用一次，函数体不重复。）
func TestVerifyWiringIsTheSharedWiringCheck(t *testing.T) {
	VerifyWiring(t, testConfig())
}

// selfGuardConfig 是 testsupport **自己**的规约：本包不写运行期状态文件，自守只做 cwd 还原
// （文件判据由 cli/server 两侧薄接线用例承担）；StateNames 因此返回 nil，InjectHint/
// RewriteHint 保留只为让 Config 结构完整、报告文案可读。
// 真正有意义的是 cwd 还原——本包的用例会 chdir 到临时目录，必须有人把它们扳回去。
//
// 为什么测试支持包也要接上自己的守卫：它自己的 chdir 用例一旦中途失败，就会把进程 cwd 留在
// 别处；没有 TestMain 记录起点，后续任何「还原兜底」都无从谈起。
func selfGuardConfig() Config {
	return Config{
		StateNames:  func(string) []string { return nil },
		ProbeNames:  nil,
		InjectHint:  "把状态文件写进 t.TempDir()。",
		RewriteHint: "把状态文件写进 t.TempDir()。",
	}
}

// TestMain 把共享守卫动态到本包（自守）。
func TestMain(m *testing.M) { os.Exit(RunMain(m.Run, selfGuardConfig())) }
