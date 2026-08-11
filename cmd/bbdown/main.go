package main

import (
	"fmt"
	"os"

	"github.com/QC3284/BBDown/internal/cli"
)

func main() {
	// 匹配原版 banner: 深蓝底白字，两行
	fmt.Print("\033[44m\033[37mBBDown version 2.0.0, Bilibili Downloader.\033[0m\n")
	fmt.Print("遇到问题请首先到以下地址查阅有无相关信息：\nhttps://github.com/QC3284/BBDown/issues\n\n")

	cli.Execute()

	defer func() {
		fmt.Print("\033[0m")
	}()
	_ = os.Stdout
}
