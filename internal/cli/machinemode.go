package cli

import "strings"

// IsMachineReadableArgs 判断本次命令行是否属于**机读模式**（`--info-json` / `--json`）。
//
// 机读模式的底线是「stdout 只有数据」：横幅、求助链接、日志都必须让位（日志走 stderr，见 SetConsoleOutput）。
// 判定放在这里而不是 main.go 里内联，是为了能单测——它是「清爽」这条界面纪律的守门人。
func IsMachineReadableArgs(args []string) bool {
	for _, a := range args {
		// 支持 --flag / --flag=true 两种写法。
		if a == "--info-json" || a == "--json" || strings.HasPrefix(a, "--info-json=") || strings.HasPrefix(a, "--json=") {
			return true
		}
	}
	return false
}
