package main

import (
	"github.com/QC3284/BBDown/internal/cli"
	"github.com/QC3284/BBDown/internal/util"
)

// bannerVersion 是横幅上的版本号（版本号硬编码点之一，见 internal/cli/version_consistency_test.go）。
const bannerVersion = "2.12.6"

// banner 是终端开场横幅：`❯ BBDown  2.12.5`。
//
// 改前是「蓝底白字的一整块」（ConsoleColor.DarkBlue 背景 + White 前景，ANSI 44 + 97）：
// 背景色把终端里别的信息全压下去，与本仓「不使用背景色」的主题冲突，也让版本号与产品名
// 同一个权重。现在 BRAND 加粗的名称 + MUTED 版本号表达同一件事——层级靠明度，不靠色块。
//
// 只有一行版本号：改前三行（版本 + 两行求助链接）挡在一切内容之前，每跑一条命令都要先滚过它。
// 求助入口没有消失——--help 与仓库 README 都有，报错提示也按错误类型给出下一步（reportError）。
//
// 打不打由 cli.Execute 决定：机读模式（--info-json / doctor --json）下不打，stdout 只留数据。
// 判定必须放在 Execute 里——它要看合并 BBDown.config 之后的参数，main 这里只看得到命令行。
// 着色能力（TTY / NO_COLOR / TERM=dumb）在这里**实时**判定：写死成包级变量的话，
// 用例注入的能力判定就再也影响不到横幅，「降级链」少一处被覆盖。
func banner() string {
	line := util.NewLine().
		Add(util.TagBrandBold, "❯ BBDown").
		Add(util.TagMuted, "  "+bannerVersion)
	return line.Render() + "\n"
}

func main() {
	cli.Execute(banner())
}
