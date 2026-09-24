package main

import (
	"fmt"

	"github.com/QC3284/BBDown/internal/cli"
	"github.com/QC3284/BBDown/internal/util"
)

func main() {
	// Banner: ConsoleColor.DarkBlue background + ConsoleColor.White text, which in
	// ANSI terms is 44 + 97 (White is the bright variant, not 37).
	fmt.Print(util.AnsiBgDarkBlue + util.AnsiWhite + "BBDown version 1.6.20-go.2, Bilibili Downloader." + util.AnsiReset + "\r\n")
	fmt.Print("遇到问题请首先到以下地址查阅有无相关信息：\r\nhttps://github.com/QC3284/BBDown/issues\r\n")
	fmt.Println()

	cli.Execute()
}
