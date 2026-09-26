package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// ansiRe 去掉横幅上的颜色码：颜色只在终端里加（管道里不加，见 main.go 的 banner），
// 用例在两种环境下都要能跑。
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestBannerIsOneCleanLine 钉住开场横幅：只有一行版本号 + 一个空行。
//
// 改前三行（版本 + 「遇到问题请首先到以下地址查阅…」+ issues 链接）挡在一切内容之前，
// 每跑一条命令都要先滚过它三行；求助入口在 --help 与 README 里，不必占开场。
//
// 变异验证：把 banner 改回三行 → 行数断言红；去掉版本号 → 版本正则断言红。
func TestBannerIsOneCleanLine(t *testing.T) {
	plain := strings.ReplaceAll(ansiRe.ReplaceAllString(banner, ""), "\r", "")
	lines := strings.Split(plain, "\n")
	if len(lines) != 3 || lines[1] != "" || lines[2] != "" {
		t.Fatalf("横幅应当是「版本行 + 一个空行」：%q", plain)
	}
	if !regexp.MustCompile(`^BBDown \d+\.\d+\.\d+$`).MatchString(lines[0]) {
		t.Errorf("首行应当只有版本号（BBDown X.Y.Z）：%q", lines[0])
	}
	for _, absent := range []string{"issues", "遇到问题", "Downloader"} {
		if strings.Contains(plain, absent) {
			t.Errorf("横幅不该再包含 %q：%q", absent, plain)
		}
	}
	// 颜色只在终端里加：管道与日志文件里留一串 ESC 只会污染下游的 grep/解析。
	// （CI 的 stdout 是管道，所以这条断言跑的就是「不着色」那一半。）
	if got, want := strings.Contains(banner, "\x1b["), util.IsTerminalOut(); got != want {
		t.Errorf("横幅是否着色应当跟随终端判定（着色=%v，终端=%v）：%q", got, want, banner)
	}
}
