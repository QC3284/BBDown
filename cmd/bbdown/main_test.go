package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// ansiRe 去掉横幅上的颜色码：颜色只在能力允许时加（管道 / NO_COLOR / TERM=dumb 都不加，
// 见 main.go 的 banner），用例在两种环境下都要能跑。
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// sgrRe 抽出文本里出现过的全部 SGR 序列。
var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// bannerFormRe 是横幅版本行的形态（剥掉颜色之后）：❯ BBDown + 两个空格 + 版本号。
var bannerFormRe = regexp.MustCompile("^❯ BBDown +[0-9]+\\.[0-9]+\\.[0-9]+$")

// TestBannerIsOneCleanLine 钉住开场横幅：只有一行版本号 + 一个空行。
//
// 改前三行（版本 + 「遇到问题请首先到以下地址查阅…」+ issues 链接）挡在一切内容之前，
// 每跑一条命令都要先滚过它三行；求助入口在 --help 与 README 里，不必占开场。
//
// 变异验证：把 banner 改回三行 → 行数断言红；去掉版本号 → 版本正则断言红。
func TestBannerIsOneCleanLine(t *testing.T) {
	plain := ansiRe.ReplaceAllString(banner(), "")
	// 不再剥离 \r：横幅此前是 "\r\n" + "\n"（CRLF 上游遗产，终端里留下 ^M，且与其余输出的 \n 混用）。
	// 现在必须是单个 \n；保留 \r 断言正是为了钉住这次修复。
	if strings.Contains(plain, "\r") {
		t.Errorf("横幅不该带 CR（混合换行）：%q", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) != 2 || lines[1] != "" {
		t.Fatalf("横幅应当是「版本行 + 一个空行」（以单个 \n 结尾）：%q", plain)
	}
	if !bannerFormRe.MatchString(lines[0]) {
		t.Errorf("首行应当是「❯ BBDown + 版本号」：%q", lines[0])
	}
	for _, absent := range []string{"issues", "遇到问题", "Downloader"} {
		if strings.Contains(plain, absent) {
			t.Errorf("横幅不该再包含 %q：%q", absent, plain)
		}
	}
	// 颜色只在能力允许时加：管道与日志文件里留一串 ESC 只会污染下游的 grep/解析。
	// （CI 的 stdout 是管道，所以这条断言跑的就是「不着色」那一半。）
	if got, want := strings.Contains(banner(), "\x1b["), util.ColorsEnabled(); got != want {
		t.Errorf("横幅是否着色应当跟随能力判定（着色=%v，允许=%v）：%q", got, want, banner())
	}
}

// TestBannerRoleColorsAreGolden 钉住横幅的角色分配与字面字节：
// **BRAND 加粗的产品名 + MUTED 的版本号，且不含任何背景色**。
//
// 改前是 ConsoleColor.DarkBlue 底 + White 字的整块反白（ANSI 44 + 97）：背景色把终端里
// 其它信息全压下去，产品名与版本号还同权重。这里的白名单断言同时挡住「背景色回来」——
// 只要出现 44/101 之类，序列就不在白名单里。
//
// 变异验证：
//   - 把版本号也改成 BRAND+BOLD（丢掉明度层级）→ MUTED 断言红；
//   - 给名称加背景色 → 白名单断言红。
func TestBannerRoleColorsAreGolden(t *testing.T) {
	t.Cleanup(util.ForceColorsForTest())

	got := banner()
	if want := "\x1b[1;38;5;45m❯ BBDown\x1b[0m"; !strings.HasPrefix(got, want) {
		t.Errorf("横幅应以 BRAND 加粗的名称开头：\n得到 %q\n期望前缀 %q", got, want)
	}
	if want := "\x1b[38;5;245m  " + bannerVersion + "\x1b[0m"; !strings.Contains(got, want) {
		t.Errorf("版本号应是 MUTED：%q", got)
	}
	allowed := map[string]bool{
		"\x1b[0m": true, "\x1b[1;38;5;45m": true, "\x1b[38;5;245m": true,
	}
	seqs := sgrRe.FindAllString(got, -1)
	if len(seqs) == 0 {
		t.Fatalf("能力允许时应给横幅着色：%q", got)
	}
	for _, seq := range seqs {
		if !allowed[seq] {
			t.Errorf("横幅出现了白名单之外的 SGR 序列 %q（背景色是禁止的）：%q", seq, got)
		}
	}
}

// TestBannerStaysPlainUnderDegradationChain：NO_COLOR / TERM=dumb / 非 TTY 三种情况下
// 横幅一个 ANSI 都不发，但版式（❯、两个空格、版本号）一字不变。
func TestBannerStaysPlainUnderDegradationChain(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		env  util.ColorEnv
	}{
		{"非 TTY", false, util.ColorEnv{Term: "xterm-256color"}},
		{"NO_COLOR=1", true, util.ColorEnv{NoColor: true, Term: "xterm-256color"}},
		{"TERM=dumb", true, util.ColorEnv{Term: "dumb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreTerm := util.SetTerminalForTest(func() bool { return c.tty })
			restoreEnv := util.SetColorEnvForTest(func() util.ColorEnv { return c.env })
			defer func() { restoreEnv(); restoreTerm() }()

			got := banner()
			if strings.Contains(got, "\x1b") {
				t.Errorf("%s 下横幅不该有 ANSI：%q", c.name, got)
			}
			if want := "❯ BBDown  " + bannerVersion; !strings.HasPrefix(got, want) {
				t.Errorf("%s 下横幅版式不符：\n得到 %q\n期望前缀 %q", c.name, got, want)
			}
		})
	}
}
