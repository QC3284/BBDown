package server

import (
	"os"
	"testing"

	"github.com/QC3284/BBDown/internal/util/testsupport"
)

// 本文件（任务 T ① 的薄接线）是「测试不把运行期状态写进工作目录」的包级守卫在 server 侧的接线。
//
// 判据（快照 / 比较 / SameDir / 还原 / 收尾报告）已经搬到 internal/util/testsupport，与
// internal/cli 共用**同一份实现**——两包原本各有一份逐字镜像（互相标注「改动一处必须同步
// 另一处」，见 docs/HANDOVER.md §5.7）。
//
// 这里只留三件本包才知道的事：
//  1. 盯哪个文件：defaultTaskFileName（bbdown-tasks.json）本尊、裸 .tmp，以及原子写中途的
//     .tmp-<jobid>（persistFinishedTasks 的临时名是 taskFile + ".tmp-" + jobID，按前缀匹配）；
//  2. 报红时给什么修复建议：s.taskFile = filepath.Join(t.TempDir(), defaultTaskFileName)；
//  3. 证明守卫真的挂在整包上：TestServerWorkdirGuardWiring 一行（主体在共享包里）。
//
// 为什么需要（背景未变）：完成任务清单的路径由 APIServer.taskFile 决定，NewAPIServer 的默认值
// defaultTaskFileName 是**相对路径**，会落到**进程工作目录**——对真实用户是设计（清单跟着 serve
// 的工作目录走），对 go test 就是污染：写出来的 bbdown-tasks.json 会出现在仓库里（2.12.1 处理过
// 同一类事故：cli 与 server 各被写过一个文件）。单条用例（guard_test.go 的
// TestBuildHandlerRejectsSimpleRequestCSRF）只能证明自己那一次调用把 taskFile 注入了临时目录，
// 管不住同包其它用例——尤其是 NewAPIServer(...) 之后忘记赋值 s.taskFile 的用例：processTask 的
// defer 会在任务收尾时把清单写进 cwd。TestMain 在整包跑完后做一次快照对比，兜住全部。

// guardConfig 是给共享守卫的薄接线：本包盯哪些文件名、报红时说什么。
func guardConfig() testsupport.Config {
	return testsupport.Config{
		StateNames: func(dir string) []string {
			return testsupport.StateNames(dir,
				[]string{defaultTaskFileName, defaultTaskFileName + ".tmp"},
				[]string{defaultTaskFileName + ".tmp-"})
		},
		ProbeNames:  []string{defaultTaskFileName, defaultTaskFileName + ".tmp-deadbeef"},
		InjectHint:  "请给 APIServer 注入临时路径（如 s.taskFile = filepath.Join(t.TempDir(), defaultTaskFileName)），不要让测试依赖进程 cwd。",
		RewriteHint: "任务清单必须写进 t.TempDir()。",
	}
}

// TestMain：整包用例跑完后，进程工作目录里不得出现（或改写）完成任务清单，cwd 不得被留在别处。
// 判据、还原与报告格式全在 testsupport（server/cli 同一份）；这里只剩「武装 + 本包的规约」。
//
// 变异验证：改共享实现的判据（快照比较、还原分支、SameDir）→ 本包的接线用例与 cli 侧同时红；
// 删掉本函数（守卫不再武装）→ TestServerWorkdirGuardWiring 红。
func TestMain(m *testing.M) { os.Exit(testsupport.RunMain(m.Run, guardConfig())) }

// TestServerWorkdirGuardWiring 是薄接线用例：证明本包真的挂上了共享守卫（起点已记录、与当前 cwd
// 同一目录），且本包的文件名规约被共享判据认下（bbdown-tasks.json 本尊与 .tmp-<jobid> 都认、
// 无关文件（coverage.out / .bak / .tmpx）不误报），共享判据也确实会判红、确实会还原偏离的 cwd。
//
// 主体在 testsupport.VerifyWiring（server/cli 共用同一个函数体），所以改共享实现会让两侧**同时**
// 红——这正是抽包要达到的效果。
func TestServerWorkdirGuardWiring(t *testing.T) { testsupport.VerifyWiring(t, guardConfig()) }
