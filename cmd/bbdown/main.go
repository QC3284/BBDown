package main

import (
	"fmt"
	"os"

	"github.com/QC3284/BBDown/internal/cli"
	"github.com/QC3284/BBDown/internal/util"
)

func main() {
	// 机读模式（--info-json / --json）不打横幅与求助链接：stdout 只留数据，管道才拿得到干净输入。
	// 判定与用例在 internal/cli/machinemode.go —— 界面纪律「清爽」的守门人。
	if !cli.IsMachineReadableArgs(os.Args[1:]) {
		// Banner: ConsoleColor.DarkBlue background + ConsoleColor.White text, which in
		// ANSI terms is 44 + 97 (White is the bright variant, not 37).
		fmt.Print(util.AnsiBgDarkBlue + util.AnsiWhite + "BBDown version 2.11.1, Bilibili Downloader." + util.AnsiReset + "\r\n")
		fmt.Print("遇到问题请首先到以下地址查阅有无相关信息：\r\nhttps://github.com/QC3284/BBDown/issues\r\n")
		fmt.Println()
	}

	cli.Execute()
}
