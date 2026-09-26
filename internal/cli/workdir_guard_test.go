package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 本文件是「测试不把运行期状态写进工作目录」的包级守卫。
//
// 为什么需要：未完成任务清单（pendingFileName）的路径由 pendingPath 从 cfg.WorkDir 推导，
// 而 WorkDir 为空时它退回**进程工作目录**——对真实用户是设计（清单就跟着工作目录走），
// 对 go test 就是污染：测试进程的 cwd 是包目录，写出来的 .bbdown-pending.json 会出现在仓库里
// （本仓真发生过，还被误提交进 git 一次）。
//
// 单条用例只能证明自己那一次调用落到了临时目录，管不住同包其它（以及以后新增的）用例；
// TestMain 在整包跑完后用 os.Getwd 做前后对比，一次兜住所有用例。

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

// TestMain：整包用例跑完后，进程工作目录里不得出现（或改写）未完成任务清单。
//
// 变异验证：把 downloadsummary_test.go 的 cfg.WorkDir 改回默认（空 = 进程工作目录）→
// 整包跑完守卫报红（清单出现在工作目录）、go test 退出码非 0；是断言红，不是构建错误。
func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "工作目录守卫：取不到进程工作目录: %v\n", err)
		os.Exit(1)
	}
	before := snapshotWorkdirArtifacts(wd)

	code := m.Run()

	polluted := false
	for name, after := range snapshotWorkdirArtifacts(wd) {
		prev, existed := before[name]
		switch {
		case !existed:
			fmt.Fprintf(os.Stderr, "工作目录守卫：%s 被测试写进了进程工作目录 %s。\n"+
				"请给被测代码注入临时目录（如 cfg.WorkDir = t.TempDir()），不要让测试依赖进程 cwd。\n",
				name, wd)
			polluted = true
		case prev.digest != after.digest || prev.modTime != after.modTime:
			fmt.Fprintf(os.Stderr, "工作目录守卫：%s 在本次测试运行中被改写（进程工作目录 %s）。\n"+
				"状态文件必须写进 t.TempDir()。\n",
				name, wd)
			polluted = true
		}
	}
	if now, err := os.Getwd(); err == nil && now != wd {
		fmt.Fprintf(os.Stderr,
			"工作目录守卫：测试结束后进程工作目录变成了 %s（原 %s）；请用 t.Chdir 让用例自己还原。\n",
			now, wd)
		polluted = true
	}
	if polluted && code == 0 {
		code = 1
	}
	os.Exit(code)
}
