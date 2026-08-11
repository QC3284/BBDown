package main

import (
	"fmt"

	"github.com/QC3284/BBDown/internal/cli"
)

func main() {
	// Banner: dark blue bg + white text (matching C# Console.BackgroundColor/Console.ForegroundColor)
	fmt.Print("\033[44m\033[37mBBDown version 2.0.0, Bilibili Downloader.\033[0m\r\n")
	fmt.Print("遇到问题请首先到以下地址查阅有无相关信息：\r\nhttps://github.com/QC3284/BBDown/issues\r\n")
	fmt.Println()

	cli.Execute()
}
