package main

import (
	"fmt"
	"os"

	"github.com/QC3284/BBDown/internal/cli"
)

func main() {
	fmt.Printf("BBDown Go - Bilibili Downloader v1.0.0\n")
	fmt.Printf("遇到问题请到 https://github.com/QC3284/BBDown/issues 查阅\n\n")

	cli.Execute()

	// Restore terminal state on exit
	defer func() {
		fmt.Print("\033[0m")
		os.Stdout.Write([]byte{})
	}()
}
