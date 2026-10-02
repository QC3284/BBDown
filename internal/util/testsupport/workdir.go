// Package testsupport 放跨包的**测试基础设施**：只在 _test.go 里被引用，不进生产路径。
//
// 当前唯一内容：包级「测试不把运行期状态写进工作目录」守卫。
//
// 背景：internal/cli（盯 .bbdown-pending.json）与 internal/server（盯 bbdown-tasks.json）
// 原本各有一份**逐字镜像**的实现（两份文件互相标注「改动一处必须同步另一处」，见
// docs/HANDOVER.md §5.7）。镜像实现的问题是判据改一处漏一处，而这条守卫守的正是
// 「测试往仓库里写状态文件」这类真事故（cli 的 pending 清单被误提交进 git 过，server 的
// taskFile 在 2.12.1 之前也被写进过包目录）。所以判据只留一份放这里，各包只剩
// 「盯哪个文件名 + 报红时给什么修复建议」的薄接线（见各包的 workdir_guard_test.go）。
//
// 守卫的两条判据（语义与抽包前逐条相同）：
//
//  1. 状态文件：包目录里出现或改写了本包的状态文件（含原子写中途的 .tmp）→ 判红。
//     快照 = 内容 sha256 + mtime：两个都要——重写一个内容不变的文件改的是 mtime，
//     只比内容会漏掉「原地改写」。
//  2. cwd 偏离：用例把进程 cwd 切走了，先 os.Chdir 还原。还原成功 → 通过（这次用例造成的
//     全局副作用已被消除），只把偏离目录记进 Report.Stray；还原失败（原目录已不存在）→ 判红
//     并给出可照做的修复建议。
//
// 判据必须与平台语义无关（AGENTS「判据必须跨平台」），所以：
//   - 「两条路径是不是同一个目录」一律 os.Stat + os.SameFile（SameDir），**禁止字符串相等**
//     ——macOS 的 /var → /private/var、Windows 的盘符大小写/8.3 短名都会让同一目录有两种写法；
//   - 「cwd 已不可读」（Linux 上目录被删后 getcwd(2) 报 ENOENT）不依赖 Getwd 的返回值，
//     判据落在「先 Chdir 还原」这个动作上；
//   - mtime 粒度（Linux ns / Windows 100ns / APFS 1ns / HFS+ 1s）用「sha256 为主判据 +
//     用例里的 +1h 位移」吸收，不依赖粒度。
package testsupport

// 「判据 × 三平台语义」表（按 AGENTS「判据必须跨平台」：写代码前先列，表里不一致的要么换成
// 与平台无关的形式，要么显式吸收差异）：
//
//	| 判据                        | Linux         | Windows        | macOS                    | 结论 |
//	|-----------------------------|---------------|----------------|--------------------------|------|
//	| 状态文件出现/消失（快照键）  | 一致          | 一致           | 一致                     | 跨平台 |
//	| 内容改写（sha256）           | 一致          | 一致           | 一致                     | 跨平台（主判据） |
//	| 文件 mtime 改写              | ns 粒度       | 100ns 粒度     | APFS 1ns / HFS+ 1s       | 与 sha256 并用；用例 +1h 位移，不依赖粒度 |
//	| cwd 是否为同一目录           | dev+ino       | 卷序列号+索引  | dev+ino（符号链接已解析） | 一律 os.Stat + os.SameFile；禁止字符串相等 |
//	| cwd 读不到（目录已删）       | getcwd ENOENT | 返回已消失路径 | name cache 保留          | 判据落在「先 Chdir 还原」；不依赖 Getwd 返回值 |
//	| 还原后复核                   | 同 Linux      | 盘符大小写/8.3 | /var → /private/var      | 复核同样走 SameDir |
//	| 创建符号链接                 | 允许          | 需特权/开发者模式 | 允许                  | 构造失败 t.Skipf 写明原因 |
//	| 删除进程当前目录             | 允许          | 不允许         | 允许                     | 构造失败 t.Skipf 写明原因 |
//
// 表里唯一的不一致是 mtime 粒度与符号链接/删目录能力：前者用「sha256 为主判据 + 用例里的
// 大幅位移」吸收，后两者显式 t.Skipf，不靠「本机跑得过」。
//
// 抽包后「谁盯哪个文件」由各包决定（见 Config.StateNames）：cli 盯 .bbdown-pending.json
// （本尊 + 裸 .tmp），server 盯 bbdown-tasks.json（本尊 + 裸 .tmp + 前缀 .tmp-<jobid>）。
import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// reportPrefix 是守卫报告的统一前缀（cli/server 抽包前逐字相同）。
const reportPrefix = "工作目录守卫"

// Config 是包级守卫的**薄接线**：各包只提供「盯什么文件名」与「报红时说什么」。
// 判据本身不在这里——它在本文件的共享实现里，两个包看到的结论必然一致。
type Config struct {
	// StateNames 返回 dir 下属于**本包运行期状态**的文件名（按名字精确匹配，不误报无关文件）。
	// 直接用 StateNames 构造器即可。
	StateNames func(dir string) []string
	// ProbeNames 是接线用例用来「造污染」的具体文件名（状态文件本尊 + 一个原子写中途名），
	// 同时用于验证本包规约确实认得它们（见 VerifyWiring）。
	ProbeNames []string
	// InjectHint 是「状态文件被写出来」时的修复建议。
	InjectHint string
	// RewriteHint 是「状态文件被原地改写」时的修复建议。
	RewriteHint string
}

// Artifact 是某个状态文件在目录里的快照：内容摘要 + 修改时间。
type Artifact struct {
	Digest  string
	ModTime int64
}

// Report 是一次收尾检查的结论。
type Report struct {
	// Stray 是用例把进程 cwd 留在的目录；cwd 没被动过时为空串。
	// 读不到 cwd（Linux 上 cwd 已被删除）时给出一段带原因的占位文本，不会是空串——
	// RunMain 靠它打印「已纠正」提示。
	Stray string
	// Problems 是判红理由（空 = 通过），每条都可直接打印。
	Problems []string
}

// workdirStart 是整包开始时的进程 cwd（RunMain 在 run 之前记录）。
//
// 守卫用例自己也要切 cwd，而它们可能排在某个「切走不还原」的用例后面：那时 os.Getwd 已经报错
// （Linux 上 cwd 指向已删除目录），连起点都取不到。用这里记录的包目录做兜底还原，
// 让守卫用例不依赖前一个用例留下的状态。
var workdirStart string

// WorkdirAtStart 返回 RunMain 记录的包目录（空 = 守卫没武装）。
func WorkdirAtStart() string { return workdirStart }

// StateNames 返回 dir 下匹配「状态文件名规约」的文件名，按名字排序（报告与用例都靠它稳定）：
// exact 精确匹配（状态文件本尊与裸 .tmp），prefixes 前缀匹配（原子写的中途名，如 .tmp-<jobid>）。
//
// 只认文件、不认目录；dir 读不了（不存在）时返回 nil。守卫必须精确——把 go test 自己生成的
// 覆盖率/性能文件算进来会误报。
func StateNames(dir string, exact, prefixes []string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		matched := false
		for _, want := range exact {
			if name == want {
				matched = true
				break
			}
		}
		if !matched {
			for _, prefix := range prefixes {
				if prefix != "" && strings.HasPrefix(name, prefix) {
					matched = true
					break
				}
			}
		}
		if matched {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Snapshot 记录 dir 下这些名字的当前状态：内容 sha256 + mtime。
// 文件不存在（或读不了）都按「没有」算——守卫只回答「本进程有没有把它写出来」。
func Snapshot(dir string, names []string) map[string]Artifact {
	out := make(map[string]Artifact, len(names))
	for _, name := range names {
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
		out[name] = Artifact{Digest: hex.EncodeToString(sum[:]), ModTime: info.ModTime().UnixNano()}
	}
	return out
}

// Problems 比较原目录（before）与跑完后的快照（after），返回「状态文件被写出来 / 被原地改写」
// 的判红理由（空 = 通过）。
//
// before/after 显式传入而不是在函数里取，是为了让用例能在临时目录上验证这条判据
// （见 TestProblemsFlagsCreatedRewrittenAndTempResidue 与两个包的接线用例）。
func Problems(dir string, before, after map[string]Artifact, cfg Config) []string {
	names := make([]string, 0, len(after))
	for name := range after {
		names = append(names, name)
	}
	sort.Strings(names) // 判红顺序稳定：同一份污染在任何一次运行里给出同样的报告

	var problems []string
	for _, name := range names {
		now := after[name]
		prev, existed := before[name]
		switch {
		case !existed:
			problems = append(problems, fmt.Sprintf(
				"%s 被测试写进了进程工作目录 %s。\n%s",
				name, dir, cfg.InjectHint))
		case prev.Digest != now.Digest || prev.ModTime != now.ModTime:
			problems = append(problems, fmt.Sprintf(
				"%s 在本次测试运行中被改写（进程工作目录 %s）。\n%s",
				name, dir, cfg.RewriteHint))
		}
	}
	return problems
}

// SameDir 判断两条路径是否指向同一个目录：true 表示「同一个目录的两种写法」，不算偏离。
//
// 为什么不能用字符串相等（2026-09，macOS CI 红的形态）：macOS 上 /var 是指向 /private/var 的
// 符号链接，os.Chdir("/var/…") 成功，但 os.Getwd() 返回解析后的 "/private/var/…"；于是
// 「os.Chdir(start) 之后 cwd 是否仍逐字等于 start」在 macOS 上必然为假，守卫在自己的语义上判红。
// Windows 的 GetCurrentDirectoryW 同样可能改写路径写法（盘符大小写/短名 8.3）。
//
// 两条路径里有任一条已不存在时（典型：用例留下的 cwd 指向被删除的目录）比不了文件标识，
// 退回「解析符号链接后的规范路径」比较；仍解析不出来就比 filepath.Clean 的结果。这条兜底只在
// 两条路径都已不可解析时命中，不会把「目录还在、只是写法不同」放过：那时走的一定是 SameFile 分支。
func SameDir(a, b string) bool {
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

// InspectAfterRun 执行整包收尾检查：先验状态文件快照，再把 cwd 扳回 start。
//
// 语义（两条判据的收尾实现）：
//   - 状态文件：start 下出现/改写状态文件 → 判红；
//   - cwd 偏离：SameDir(now, start) 为真（同一目录的不同写法不算偏离）→ 通过，Stray 留空；
//     os.Chdir(start) 能还原 → 通过，只把偏离目录记进 Stray；
//     还原不了（start 已不存在）→ 判红，并给出可执行的修复建议。
//
// start 与 before 显式传入（不是函数内部读进程状态），这样两条路径都能在用例里用临时目录复现。
func InspectAfterRun(start string, before map[string]Artifact, cfg Config) Report {
	rep := Report{Problems: Problems(start, before, Snapshot(start, cfg.StateNames(start)), cfg)}

	now, nowErr := os.Getwd()
	// 不是逐字比较：macOS 上 /var → /private/var 这类符号链接会让同一个目录有两个写法，
	// 逐字比较会把「没有偏离」判成偏离（见 SameDir）。
	if nowErr == nil && SameDir(now, start) {
		return rep
	}
	if nowErr == nil {
		rep.Stray = now
	} else {
		// Linux：cwd 指向已删除目录时 getcwd(2) 报 ENOENT，报不出路径也不能因此放过。
		rep.Stray = fmt.Sprintf("<读不到 cwd: %v>", nowErr)
	}

	if err := os.Chdir(start); err != nil {
		rep.Problems = append(rep.Problems, fmt.Sprintf(
			"用例把进程工作目录切到了 %s，且无法还原到原目录：%v。\n"+
				"修复建议：确认原目录存在且可进入——cd %s（或 os.Chdir(%q)）；\n"+
				"用例本身请改用 t.Chdir(dir)，由 testing 包负责还原，不要在测试里裸 os.Chdir。",
			rep.Stray, err, start, start))
		return rep
	}
	if got, gerr := os.Getwd(); gerr != nil || !SameDir(got, start) {
		rep.Problems = append(rep.Problems, fmt.Sprintf(
			"用例把进程工作目录切到了 %s；os.Chdir(%q) 之后 cwd 仍是 %q（err=%v），还原未生效。",
			rep.Stray, start, got, gerr))
	}
	return rep
}

// RunMain 是各包 TestMain 的共享主体：记录起点 → 跑用例 → 收尾检查 → 打印报告并返回退出码。
//
// 各包的 TestMain 只剩一行：
//
//	func TestMain(m *testing.M) { os.Exit(testsupport.RunMain(m.Run, guardConfig())) }
//
// run 取 func() int（就是 m.Run）而不是 *testing.M：本包因此不 import testing，
// 「测试支持包」不会把 testing 链进任何非测试产物；同时也让 RunMain 自己可被用例驱动。
func RunMain(run func() int, cfg Config) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s：取不到进程工作目录: %v\n", reportPrefix, err)
		return 1
	}
	workdirStart = wd
	before := Snapshot(wd, cfg.StateNames(wd))

	code := run()

	rep := InspectAfterRun(wd, before, cfg)
	// 偏离但已还原：通过，只在报告里列出被纠正的目录（该用例的全局副作用已经消除）。
	if rep.Stray != "" && len(rep.Problems) == 0 {
		fmt.Fprintf(os.Stderr,
			"%s：用例把进程工作目录切到了 %s，已还原为 %s。\n"+
				"该用例请改用 t.Chdir(dir)（testing 包负责还原），不要裸 os.Chdir 后不还原。\n",
			reportPrefix, rep.Stray, wd)
	}
	for _, p := range rep.Problems {
		fmt.Fprintf(os.Stderr, "%s：%s\n", reportPrefix, p)
	}
	if len(rep.Problems) > 0 && code == 0 {
		code = 1
	}
	return code
}

// Chdir 把进程 cwd 切到 dir，返回还原函数（调用方负责在用例结束时调用，通常 t.Cleanup(restore)）。
//
// 还原目标优先用「进入时的 cwd」：这样前面用例留下的偏离不会被本用例的 cleanup 悄悄抹掉，
// 守卫仍能报出「被纠正的用例目录」。只有起点已经不可读时（前一个用例把 cwd 留在已删除目录，
// Linux 上 os.Getwd 直接报错）才退到 RunMain 记下的包目录。
//
// 还原失败只忽略：真正的判红由收尾检查给出（这里再报一次只会重复）。
func Chdir(dir string) (func(), error) {
	start, err := os.Getwd()
	if err != nil {
		start = workdirStart
	}
	if start == "" {
		return nil, errors.New("取不到可用于还原的工作目录")
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	return func() { _ = os.Chdir(start) }, nil
}
