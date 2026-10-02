package cli

import (
	"os"
	"testing"

	"github.com/QC3284/BBDown/internal/util/testsupport"
)

// 本文件（任务 T ①/c 的薄接线）是「测试不把运行期状态写进工作目录」的包级守卫在 cli 侧的接线。
//
// 判据（快照 / 比较 / SameDir / 还原 / 收尾报告）已经搬到 internal/util/testsupport，与
// internal/server 共用**同一份实现**——两包原本各有一份逐字镜像（互相标注「改动一处必须同步
// 另一处」，见 docs/HANDOVER.md §5.7），改一处漏一处正是这类守卫最危险的失效方式。
//
// 这里只留三件本包才知道的事：
//  1. 盯哪个文件：pendingFileName（.bbdown-pending.json）及其原子写中途的 .tmp；
//  2. 报红时给什么修复建议：cfg.WorkDir = t.TempDir()；
//  3. 证明守卫真的挂在整包上：TestWorkdirGuardWiring 一行（主体在共享包里）。
//
// 为什么需要（背景未变）：未完成任务清单（pendingFileName）的路径由 pendingPath 从 cfg.WorkDir
// 推导，而 WorkDir 为空时它退回**进程工作目录**——对真实用户是设计（清单就跟着工作目录走），
// 对 go test 就是污染：测试进程的 cwd 是包目录，写出来的 .bbdown-pending.json 会出现在仓库里
// （本仓真发生过，还被误提交进 git 一次）。单条用例只能证明自己那一次调用落到了临时目录，
// 管不住同包其它（以及以后新增的）用例；TestMain 在整包跑完后做一次前后快照对比，一次兜住所有。

// guardConfig 是给共享守卫的薄接线：本包盯哪些文件名、报红时说什么。
func guardConfig() testsupport.Config {
	return testsupport.Config{
		StateNames: func(dir string) []string {
			return testsupport.StateNames(dir, []string{pendingFileName, pendingFileName + ".tmp"}, nil)
		},
		ProbeNames:  []string{pendingFileName, pendingFileName + ".tmp"},
		InjectHint:  "请给被测代码注入临时目录（如 cfg.WorkDir = t.TempDir()），不要让测试依赖进程 cwd。",
		RewriteHint: "状态文件必须写进 t.TempDir()。",
	}
}

// TestMain：整包用例跑完后，进程工作目录里不得出现（或改写）未完成任务清单，cwd 不得被留在别处。
// 判据、还原与报告格式全在 testsupport（cli/server 同一份）；这里只剩「武装 + 本包的规约」。
//
// 变异验证：改共享实现的判据（快照比较、还原分支、SameDir）→ 本包的接线用例与 server 侧同时红。
func TestMain(m *testing.M) { os.Exit(testsupport.RunMain(m.Run, guardConfig())) }

// TestWorkdirGuardWiring 是薄接线用例：证明本包真的挂上了共享守卫（起点已记录、与当前 cwd 同一
// 目录），且本包的文件名规约被共享判据认下（认下本包的 pending 清单、不误报无关文件），
// 共享判据也确实会判红、确实会还原偏离的 cwd。
//
// 主体在 testsupport.VerifyWiring（cli/server 共用同一个函数体，没有第二份镜像），所以改共享
// 实现会让两侧**同时**红——这正是抽包要达到的效果。
func TestWorkdirGuardWiring(t *testing.T) { testsupport.VerifyWiring(t, guardConfig()) }
