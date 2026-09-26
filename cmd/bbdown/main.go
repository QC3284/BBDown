package main

import (
	"github.com/QC3284/BBDown/internal/cli"
	"github.com/QC3284/BBDown/internal/util"
)

// banner 是终端开场横幅（版本号硬编码点之一，见 internal/cli/version_consistency_test.go）。
//
// 只有一行版本号：改前三行（版本 + 两行求助链接）挡在一切内容之前，每跑一条命令都要先滚过它。
// 求助入口没有消失——--help 与仓库 README 都有，报错提示也按错误类型给出下一步（reportError）。
//
// 颜色只在终端里加（Banner: ConsoleColor.DarkBlue 背景 + ConsoleColor.White 前景，ANSI 44 + 97，
// White 是亮色变体而不是 37）：管道与日志文件里留下一串 ESC 序列只会污染下游的 grep/解析。
//
// 打不打由 cli.Execute 决定：机读模式（--info-json / doctor --json）下不打，stdout 只留数据。
// 判定必须放在 Execute 里——它要看合并 BBDown.config 之后的参数，main 这里只看得到命令行。
var banner = func() string {
	line := "BBDown 2.12.5"
	if util.IsTerminalOut() {
		line = util.AnsiBgDarkBlue + util.AnsiWhite + line + util.AnsiReset
	}
	return line + "\r\n" + "\n"
}()

func main() {
	cli.Execute(banner)
}
