package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 本文件是「测试不把运行期状态写进工作目录」的包级守卫，外加守卫自身语义的用例。
//
// 为什么需要：未完成任务清单（pendingFileName）的路径由 pendingPath 从 cfg.WorkDir 推导，
// 而 WorkDir 为空时它退回**进程工作目录**——对真实用户是设计（清单就跟着工作目录走），
// 对 go test 就是污染：测试进程的 cwd 是包目录，写出来的 .bbdown-pending.json 会出现在仓库里
// （本仓真发生过，还被误提交进 git 一次）。
//
// 单条用例只能证明自己那一次调用落到了临时目录，管不住同包其它（以及以后新增的）用例；
// TestMain 在整包跑完后用 os.Getwd 做前后对比，一次兜住所有用例。
//
// 收尾判据两条，各自都有本文件的用例钉住：
//
//  1. 状态文件（pendingArtifactProblems）：包目录里出现或改写了 .bbdown-pending.json（含 .tmp）→ 判红。
//     这是守卫存在的理由，任何情况下不放松。
//  2. cwd 偏离（inspectWorkdirAfterRun）：用例把进程 cwd 切走了，先 os.Chdir 还原。
//     还原成功 → 通过（这次用例造成的全局副作用已被消除），只在报告里点出被纠正的目录；
//     还原失败（原目录已不存在）→ 判红，并打印可执行的修复建议。
//
// 判据 2 为什么不能直接比字符串（2026-09 修订）：旧版要求「os.Getwd() 与开始时逐字相同」，
// 这条在 Windows/macOS 上不成立——用例把 cwd 切进临时目录后该目录被 RemoveAll 掉，
// Windows 的 GetCurrentDirectoryW 与 macOS 的 getcwd(2) 仍返回那个已消失的路径，
// 于是 CI 红在一个与被测行为无关的平台细节上；Linux 的 getcwd(2) 返回 ENOENT
// （旧判据被 err == nil 短路），ubuntu 反而绿。源头修复见 pending_wire_test.go：
// 守住源头才是义务，还原只是兜底。
//
// 同一目录的**写法**同样不能比字符串（2026-09 第二次修订，这次红在守卫自己身上）：
// macOS 上 /var 是指向 /private/var 的符号链接，os.Chdir("/var/…") 成功，os.Getwd() 却返回
// 解析后的 "/private/var/…"；于是「还原后是否仍等于传进来的那个字符串」在 macOS 上必然为假，
// 守卫把一个「已还原、目录还在」的正常收尾判成了红。凡「两条路径是不是同一个目录」的判定
// 一律走 sameDir（os.Stat + os.SameFile）：判据与平台语义无关，符号链接、/var 这类别名、
// Windows 的盘符大小写都不再影响结论。

// workdirAtStart 是整包开始时的进程 cwd（TestMain 在 m.Run 之前记录）。
//
// 守卫用例自己也要切 cwd，而它们可能排在某个「切走不还原」的用例后面：那时 os.Getwd 已经报错
// （Linux 上 cwd 指向已删除目录），连起点都取不到。用这里记录的包目录做兜底还原，
// 让守卫用例不依赖前一个用例留下的状态。
var workdirAtStart string

// workdirArtifact 是某个状态文件在 cwd 里的快照：内容摘要 + 修改时间。
// 两个都要：重写一个内容不变的文件（upsertPending 会保留并刷新同 URL 条目）改的是 mtime，
// 只比内容会漏掉「原地改写」。
type workdirArtifact struct {
	digest  string
	modTime int64
}

// snapshotWorkdirArtifacts 记录 dir 下未完成任务清单（含写入用的 .tmp）的当前状态。
//
// 只盯这两个名字：守卫要精确，不能把 go test 自己生成的覆盖率/性能文件也算进来。文件不
// 存在（或读不了）都按「没有」算——守卫只回答「本进程有没有把它写出来」。
func snapshotWorkdirArtifacts(dir string) map[string]workdirArtifact {
	out := make(map[string]workdirArtifact, 2)
	for _, name := range []string{pendingFileName, pendingFileName + ".tmp"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out[name] = workdirArtifact{digest: hex.EncodeToString(sum[:]), modTime: info.ModTime().UnixNano()}
	}
	return out
}

// pendingArtifactProblems 比较守卫原目录（before）与跑完后的快照（after），
// 返回「清单文件被写出来 / 被原地改写」的判红理由（空 = 通过）。
//
// before/after 显式传入而不是在函数里取，是为了让用例能在临时目录上验证这条判据
// （见 TestWorkdirGuardFlagsPendingArtifacts）。
func pendingArtifactProblems(dir string, before, after map[string]workdirArtifact) []string {
	var problems []string
	for name, now := range after {
		prev, existed := before[name]
		switch {
		case !existed:
			problems = append(problems, fmt.Sprintf(
				"%s 被测试写进了进程工作目录 %s。\n"+
					"请给被测代码注入临时目录（如 cfg.WorkDir = t.TempDir()），不要让测试依赖进程 cwd。",
				name, dir))
		case prev.digest != now.digest || prev.modTime != now.modTime:
			problems = append(problems, fmt.Sprintf(
				"%s 在本次测试运行中被改写（进程工作目录 %s）。\n"+
					"状态文件必须写进 t.TempDir()。",
				name, dir))
		}
	}
	return problems
}

// sameDir 判断两条路径是否指向同一个目录：true 表示「同一个目录的两种写法」，不算偏离。
//
// 为什么不能用字符串相等（2026-09，macOS CI 红的形态）：macOS 上 /var 是指向 /private/var 的
// 符号链接，os.Chdir("/var/…") 成功，但 os.Getwd() 返回解析后的 "/private/var/…"；于是
// 「os.Chdir(start) 之后 cwd 是否仍逐字等于 start」在 macOS 上必然为假，守卫在自己的语义上判红。
// Windows 的 GetCurrentDirectoryW 同样可能改写路径写法（盘符大小写/短名 8.3）。字符串相等是
// 平台相关的判据；这里改为平台语义无关的做法：先 os.Stat 两边，用 os.SameFile 比
// inode（Unix）/ 文件索引（Windows）。
//
// 两条路径里有任一条已不存在时（典型：用例留下的 cwd 指向被删除的目录，Linux 上 os.Getwd 直接
// 报错）比不了文件标识，退回「解析符号链接后的规范路径」比较；仍解析不出来就比 filepath.Clean
// 的结果。这条兜底只在两条路径都已不可解析时命中，不会把「目录还在、只是写法不同」放过：
// 那时走的一定是 SameFile 分支。
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if ai, aerr := os.Stat(a); aerr == nil {
		if bi, berr := os.Stat(b); berr == nil {
			return os.SameFile(ai, bi)
		}
	}
	return resolveDirPath(a) == resolveDirPath(b)
}

// resolveDirPath 尽力把 dir 归一化成可比较的形式：能解析符号链接就解析，否则退回 Clean。
func resolveDirPath(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// workdirGuardReport 是一次收尾检查的结论。
type workdirGuardReport struct {
	// stray 是用例把进程 cwd 留在的目录；cwd 没被动过时为空串。
	// 读不到 cwd（Linux 上 cwd 已被删除）时给出一段带原因的占位文本，不会是空串——
	// TestMain 靠它打印「已纠正」提示。
	stray string
	// problems 是判红理由（空 = 通过），每条都可直接打印。
	problems []string
}

// inspectWorkdirAfterRun 执行整包收尾检查：先验状态文件快照，再把 cwd 扳回 start。
//
// 语义（两条判据的收尾实现）：
//   - 状态文件：start 下出现/改写清单文件 → 判红；
//   - cwd 偏离：sameDir(now, start) 为真（同一目录的不同写法不算偏离）→ 通过，stray 留空；
//     os.Chdir(start) 能还原 → 通过，只把偏离目录记进 stray；
//     还原不了（start 已不存在）→ 判红，并给出可执行的修复建议。
//
// start 与 before 显式传入（不是函数内部读进程状态），这样两条路径都能在同包用例里用
// 临时目录复现：TestWorkdirGuardRestoresStrayWorkdir、TestWorkdirGuardAcceptsSymlinkedSpelling、
// TestWorkdirGuardFailsWhenStartDirGone、TestWorkdirGuardRestoresFromDeletedWorkdir。
func inspectWorkdirAfterRun(start string, before map[string]workdirArtifact) workdirGuardReport {
	rep := workdirGuardReport{
		problems: pendingArtifactProblems(start, before, snapshotWorkdirArtifacts(start)),
	}

	now, nowErr := os.Getwd()
	// 不是逐字比较：macOS 上 /var → /private/var 这类符号链接会让同一个目录有两个写法，
	// 逐字比较会把「没有偏离」判成偏离（见 sameDir）。
	if nowErr == nil && sameDir(now, start) {
		return rep
	}
	if nowErr == nil {
		rep.stray = now
	} else {
		// Linux：cwd 指向已删除目录时 getcwd(2) 报 ENOENT，报不出路径也不能因此放过。
		rep.stray = fmt.Sprintf("<读不到 cwd: %v>", nowErr)
	}

	if err := os.Chdir(start); err != nil {
		rep.problems = append(rep.problems, fmt.Sprintf(
			"用例把进程工作目录切到了 %s，且无法还原到原目录：%v。\n"+
				"修复建议：确认原目录存在且可进入——cd %s（或 os.Chdir(%q)）；\n"+
				"用例本身请改用 t.Chdir(dir)，由 testing 包负责还原，不要在测试里裸 os.Chdir。",
			rep.stray, err, start, start))
		return rep
	}
	if got, gerr := os.Getwd(); gerr != nil || !sameDir(got, start) {
		rep.problems = append(rep.problems, fmt.Sprintf(
			"用例把进程工作目录切到了 %s；os.Chdir(%q) 之后 cwd 仍是 %q（err=%v），还原未生效。",
			rep.stray, start, got, gerr))
	}
	return rep
}

// TestMain：整包用例跑完后，进程工作目录里不得出现（或改写）未完成任务清单。
//
// 变异验证（两条判据各自可被撤掉）：
//   - 撤掉 inspectWorkdirAfterRun 里的 os.Chdir 还原 → TestWorkdirGuardRestoresStrayWorkdir /
//     TestWorkdirGuardRestoresFromDeletedWorkdir 变红；
//   - 撤掉 pendingArtifactProblems 的调用（或让它恒返回 nil）→ TestWorkdirGuardFlagsPendingArtifacts 变红；
//   - 把 sameDir 改回字符串比较 → TestWorkdirGuardAcceptsSymlinkedSpelling 变红
//     （这正是 2026-09 macOS CI 红的形态）。
func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "工作目录守卫：取不到进程工作目录: %v\n", err)
		os.Exit(1)
	}
	workdirAtStart = wd
	before := snapshotWorkdirArtifacts(wd)

	code := m.Run()

	rep := inspectWorkdirAfterRun(wd, before)
	// 偏离但已还原：通过，只在报告里列出被纠正的目录（该用例的全局副作用已经消除）。
	if rep.stray != "" && len(rep.problems) == 0 {
		fmt.Fprintf(os.Stderr,
			"工作目录守卫：用例把进程工作目录切到了 %s，已还原为 %s。\n"+
				"该用例请改用 t.Chdir(dir)（testing 包负责还原），不要裸 os.Chdir 后不还原。\n",
			rep.stray, wd)
	}
	for _, p := range rep.problems {
		fmt.Fprintf(os.Stderr, "工作目录守卫：%s\n", p)
	}
	if len(rep.problems) > 0 && code == 0 {
		code = 1
	}
	os.Exit(code)
}

// chdirForTest 把进程 cwd 切到 dir，并保证本用例结束时切回进入时的目录。
//
// 不用 t.Chdir：本文件的用例要复现的正是「用例自己 os.Chdir 却不还原」这一场景，
// 守卫的兜底语义才有被测对象。
//
// 还原目标优先用「进入本用例时的 cwd」：这样前面用例留下的偏离不会被本文件的 cleanup
// 悄悄抹掉，守卫仍能报出「被纠正的用例目录」。只有起点已经不可读时（前一个用例把 cwd 留在
// 已删除目录，Linux 上 os.Getwd 直接报错）才退到 TestMain 记下的包目录。
// 注册的 cleanup 在 t.TempDir 的删除 cleanup 之前跑（LIFO），
// 所以不会出现「删不掉当前目录」的平台错误。
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	start, err := os.Getwd()
	if err != nil {
		start = workdirAtStart
	}
	if start == "" {
		t.Fatal("取不到可用于还原的工作目录")
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(start) })
}

// TestWorkdirGuardRestoresStrayWorkdir：用例把 cwd 切到别处（目录还在）时，
// 守卫必须把它还原回来并通过，同时在报告里给出被纠正的目录。
//
// 变异验证：去掉 inspectWorkdirAfterRun 里的 os.Chdir 还原 → 本用例红。
func TestWorkdirGuardRestoresStrayWorkdir(t *testing.T) {
	base := t.TempDir()
	pkg := filepath.Join(base, "pkg") // 模拟包目录（会话开始时的原目录）
	stray := filepath.Join(base, "bbdown-pending-stray")
	for _, dir := range []string{pkg, stray} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	chdirForTest(t, stray)

	rep := inspectWorkdirAfterRun(pkg, map[string]workdirArtifact{})
	if len(rep.problems) != 0 {
		t.Fatalf("原目录还在、能还原，就不该判红，实际：%v", rep.problems)
	}
	if !sameDir(rep.stray, stray) {
		t.Fatalf("报告要列出被纠正的用例目录：stray = %q，want %q", rep.stray, stray)
	}
	if now, err := os.Getwd(); err != nil || !sameDir(now, pkg) {
		t.Fatalf("cwd 应当被还原到 %s，实际 %q（err=%v）", pkg, now, err)
	}
}

// TestWorkdirGuardAcceptsSymlinkedSpelling：同一目录的两种写法（符号链接路径 vs 解析后路径）
// 必须被认定为同一个目录 → 不判红。这是 macOS 上 /var → /private/var 的跨平台复现：
// os.Chdir("/var/…") 成功，os.Getwd() 却返回 "/private/var/…"，逐字比较在此必然失败。
//
// 构造：os.Symlink 出一个指向真实目录的链接，从符号链接路径 chdir 进去（Getwd 给出解析后的
// 路径），而 start 分别按「解析后路径」与「符号链接写法」传入，覆盖两个方向。
//
// Windows 默认不允许普通用户创建符号链接（需要开发者模式 / SeCreateSymbolicLinkPrivilege），
// 构造不出来就 t.Skip 并写明原因；Linux/macOS 上本用例必须跑。
//
// 变异验证：把 sameDir 改回字符串比较 → 本用例红（步骤 ③ 正是 CI 上那条失败形态）。
func TestWorkdirGuardAcceptsSymlinkedSpelling(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "pkg-real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "pkg-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本平台不允许创建符号链接（%v）：跳过「同一目录的不同写法」场景", err)
	}

	// helper 单测：符号链接写法与解析后写法是同一目录；不同目录必须判否（防 helper 恒真）。
	if !sameDir(link, real) {
		t.Fatalf("sameDir(%q, %q) = false，同一目录的两种写法必须判为相同", link, real)
	}
	if sameDir(real, filepath.Join(base, "elsewhere")) {
		t.Fatalf("sameDir(%q, %q) = true，不同目录必须判否", real, filepath.Join(base, "elsewhere"))
	}

	// ① cwd 经符号链接进入、start 记为解析后路径：Getwd 返回解析后的路径，与 start 是同一目录。
	chdirForTest(t, link)
	rep := inspectWorkdirAfterRun(real, map[string]workdirArtifact{})
	if len(rep.problems) != 0 {
		t.Fatalf("同一目录的两种写法不该判红，实际：%v", rep.problems)
	}
	if rep.stray != "" {
		t.Fatalf("同一目录不算偏离，stray 应为空，实际 %q", rep.stray)
	}

	// ② 反向：start 用符号链接写法、Getwd 返回解析后路径——macOS /var → /private/var 的形态。
	rep = inspectWorkdirAfterRun(link, map[string]workdirArtifact{})
	if len(rep.problems) != 0 {
		t.Fatalf("start 用符号链接写法、cwd 被解析，不该判红，实际：%v", rep.problems)
	}
	if rep.stray != "" {
		t.Fatalf("同一目录不算偏离，stray 应为空，实际 %q", rep.stray)
	}

	// ③ 真偏离 + 用符号链接写法还原：os.Chdir(link) 会成功，但复核时 Getwd 给出解析后路径，
	//    还原后的校验同样必须走 sameDir（macOS CI 上判红的正是这一条）。
	stray := filepath.Join(base, "bbdown-pending-stray")
	if err := os.Mkdir(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, stray)
	rep = inspectWorkdirAfterRun(link, map[string]workdirArtifact{})
	if len(rep.problems) != 0 {
		t.Fatalf("原目录还在、能还原，就不该判红，实际：%v", rep.problems)
	}
	if !sameDir(rep.stray, stray) {
		t.Fatalf("报告要列出被纠正的用例目录：stray = %q，want %q", rep.stray, stray)
	}
	if now, err := os.Getwd(); err != nil || !sameDir(now, real) {
		t.Fatalf("cwd 应当被还原到 %q，实际 %q（err=%v）", real, now, err)
	}
}

// TestWorkdirGuardFailsWhenStartDirGone：原目录已不存在时还原必然失败——此时必须判红，
// 且报红文本要给出原目录路径（可照做的修复建议）。
//
// 变异验证：去掉还原失败分支的判红 → 本用例红。
func TestWorkdirGuardFailsWhenStartDirGone(t *testing.T) {
	base := t.TempDir()
	gone := filepath.Join(base, "pkg") // 原目录，稍后删除；它不是 cwd，Windows 也删得掉
	stray := filepath.Join(base, "bbdown-pending-stray")
	for _, dir := range []string{gone, stray} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	chdirForTest(t, stray)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	rep := inspectWorkdirAfterRun(gone, map[string]workdirArtifact{})
	if len(rep.problems) == 0 {
		t.Fatal("原目录已不存在、还原失败，必须判红")
	}
	if joined := strings.Join(rep.problems, "\n"); !strings.Contains(joined, gone) {
		t.Fatalf("报红文本要给出原目录路径 %s，实际：%s", gone, joined)
	}
}

// TestWorkdirGuardRestoresFromDeletedWorkdir：跨平台复现 CI 上那条路径——
// os.Chdir(tmp) 之后 os.Remove(tmp)，cwd 指向一个已被删除的目录。
// 断言两条分支：原目录还在 → 还原并通过；原目录也没了 → 判红。
//
// Linux 的 getcwd(2) 此时返回 ENOENT（读不到 cwd），Windows/macOS 返回那个已消失的路径；
// 两条分支都不依赖具体返回值，而是落在「先 os.Chdir 还原」这一动作上。
func TestWorkdirGuardRestoresFromDeletedWorkdir(t *testing.T) {
	base := t.TempDir()
	pkg := filepath.Join(base, "pkg")
	if err := os.Mkdir(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, pkg)

	// deletedWorkdir 建一个临时目录、切进去、再删掉它——构造「cwd 指向已删除目录」。
	deletedWorkdir := func() string {
		t.Helper()
		dir, err := os.MkdirTemp("", "bbdown-guard-deleted-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dir); err != nil {
			// Windows 不允许删除进程当前目录，构造不出这一情形（CI 上该平台也因此才红）。
			t.Skipf("本平台不允许删除进程当前目录（%v）：跳过「cwd 指向已删除目录」场景", err)
		}
		return dir
	}

	// ① 能还原 → 通过：原目录还在，守卫必须先把它切回去。
	deletedWorkdir()
	rep := inspectWorkdirAfterRun(pkg, map[string]workdirArtifact{})
	if len(rep.problems) != 0 {
		t.Fatalf("原目录还在，还原后应当通过，实际：%v", rep.problems)
	}
	if rep.stray == "" {
		t.Fatal("cwd 曾被切走，报告里必须出现被纠正的目录（哪怕读不到路径）")
	}
	if now, err := os.Getwd(); err != nil || !sameDir(now, pkg) {
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
	rep = inspectWorkdirAfterRun(gone, map[string]workdirArtifact{})
	if len(rep.problems) == 0 {
		t.Fatal("原目录已删除、还原失败，必须判红")
	}
	if joined := strings.Join(rep.problems, "\n"); !strings.Contains(joined, gone) {
		t.Fatalf("报红文本要给出原目录路径 %s，实际：%s", gone, joined)
	}
}

// TestWorkdirGuardFlagsPendingArtifacts：原 cwd 里出现（或原地改写）清单文件一律判红——
// 这是本守卫存在的理由（.bbdown-pending.json 曾被误提交进 git），与 cwd 是否被改过无关。
//
// 变异验证：让 pendingArtifactProblems 恒返回 nil（等于撤掉文件快照判据）→ 本用例红。
func TestWorkdirGuardFlagsPendingArtifacts(t *testing.T) {
	dir := t.TempDir()
	before := snapshotWorkdirArtifacts(dir)
	if len(before) != 0 {
		t.Fatalf("干净目录不该有清单文件，实际 %v", before)
	}

	path := filepath.Join(dir, pendingFileName)
	if err := os.WriteFile(path, []byte("[{\"url\":\"BV1xx411c7mD\"}]"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := snapshotWorkdirArtifacts(dir)
	problems := pendingArtifactProblems(dir, before, created)
	if len(problems) == 0 {
		t.Fatal("清单文件出现在工作目录时必须判红")
	}
	if !strings.Contains(problems[0], pendingFileName) || !strings.Contains(problems[0], dir) {
		t.Fatalf("报红要指出文件名与目录，实际 %q", problems)
	}

	// 内容不变、只刷新 mtime（upsertPending 对同 URL 条目会原地改写）同样判红。
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if len(pendingArtifactProblems(dir, created, snapshotWorkdirArtifacts(dir))) == 0 {
		t.Fatal("清单被原地改写（内容不变、mtime 变化）时必须判红")
	}

	// 原子写中途留下的 .tmp 也是污染：它会随仓库一起被提交/被 diff 看到。
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(pendingArtifactProblems(dir, created, snapshotWorkdirArtifacts(dir))) == 0 {
		t.Fatal("清单的 .tmp 残留必须判红")
	}
}
