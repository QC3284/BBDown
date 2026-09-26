package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// 上游（Spectre.Console.Cli）的两种失败输出泾渭分明：
//   - 参数解析失败 → 错误 + 帮助文本；
//   - 运行期异常 → SetExceptionHandler：只打印异常消息与一句升级提示，绝不打印帮助。
//
// cobra 默认对**任何** RunE 错误都打印 usage（还有自带的 "Error: " 前缀），
// 用户实测到的就是「非命令错误，执行失败时也会弹出帮助」。
//
// 本仓在保持上游语义的同时把失败块排成**有序三段**（改前是「原因 + 一句固定升级提示」，
// 无论 412、断网还是目录不可写，用户拿到的下一步都一样）：
//  1. 红底标题行（上游配色）：原因；
//  2. 第二行：按错误类型给建议——412 风控 / 网络 / 参数 / 读写权限 / 登录态 各一条；
//  3. 第三行：可以直接复制的命令（如 bbdown login / bbdown doctor），给不出就省略。
//
// 上游那句固定句只在**归不了类**时保留（见 TestReportErrorFallbackKeepsUpgradeHint）。

// reportLines 跑一遍 reportError，把输出按行拆开（不裁掉行内空格，列对齐要靠它）。
func reportLines(err error) []string {
	var buf bytes.Buffer
	reportError(err, &buf)
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

// TestReportErrorThreeOrderedSegments 是失败块的主用例：每类错误一条，逐条钉住
// 「原因不丢 + 建议对类 + 命令可复制」。
//
// 变异验证：撤掉 errorAdvice 里任一分类分支（例如网络那条 case），该类的错误会落到兜底句，
// 「建议必须含指定关键词」与「不得等于升级提示」两条断言变红——这正是旧行为被改回来时的退化。
func TestReportErrorThreeOrderedSegments(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantReason  string // 必须原样留在标题行里
		wantAdvice  string // 建议行必须含的关键词
		wantCommand string // 第三行（含「命令：」前缀的完整行）
	}{
		{
			name:        "412风控",
			err:         errors.New("解析视频信息失败: HTTP 412: download failed（疑似风控拦截：请等待数分钟至数十分钟后重试，或更换网络出口；持续重试会加重风控）"),
			wantReason:  "HTTP 412",
			wantAdvice:  "风控",
			wantCommand: "命令：bbdown doctor",
		},
		{
			name:        "网络",
			err:         errors.New("解析视频信息失败: Get \"https://api.bilibili.com/x/web-interface/view\": dial tcp 45.113.201.14:443: connect: connection refused"),
			wantReason:  "connection refused",
			wantAdvice:  "网络",
			wantCommand: "命令：bbdown doctor",
		},
		{
			name:        "参数",
			err:         errors.New("解析链接失败: 输入有误：无法识别的视频 URL 或 ID"),
			wantReason:  "无法识别的视频 URL 或 ID",
			wantAdvice:  "参数",
			wantCommand: "命令：bbdown --help",
		},
		{
			name:        "读写权限",
			err:         errors.New("mkdir /srv/media/BBDown: permission denied"),
			wantReason:  "permission denied",
			wantAdvice:  "可写",
			wantCommand: "命令：bbdown doctor",
		},
		{
			name:        "登录态",
			err:         errors.New("此视频需要大会员登录才能获取完整内容"),
			wantReason:  "大会员",
			wantAdvice:  "登录",
			wantCommand: "命令：bbdown login",
		},
	}

	advices := map[string]string{} // 建议行 → 用例名：两类共用一条建议就说明分类塌了
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines := reportLines(c.err)
			if len(lines) != 3 {
				t.Fatalf("失败块应是三段（原因/建议/命令），实际 %d 行：%q", len(lines), lines)
			}
			if !strings.Contains(lines[0], c.wantReason) {
				t.Errorf("标题行必须保留原因 %q，实际 %q", c.wantReason, lines[0])
			}
			// 上游把异常行设成白字红底（亮色档 101/97）；行尾必须复位颜色，否则污染后续输出。
			if !strings.HasPrefix(lines[0], util.AnsiBgRed+util.AnsiWhite) || !strings.HasSuffix(lines[0], util.AnsiReset) {
				t.Errorf("标题行要保持白字红底并复位颜色：%q", lines[0])
			}
			// 建议与命令从同一列开始（两个字 + 全角冒号），扫起来才有秩序。
			if !strings.HasPrefix(lines[1], "提示：") {
				t.Fatalf("第二行应以「提示：」开头，实际 %q", lines[1])
			}
			advice := strings.TrimPrefix(lines[1], "提示：")
			if !strings.Contains(advice, c.wantAdvice) {
				t.Errorf("建议应含 %q，实际 %q", c.wantAdvice, advice)
			}
			if advice == upgradeHint {
				t.Errorf("这一类错误落到了兜底句（分类分支被撤掉或未命中）：%q", advice)
			}
			if lines[2] != c.wantCommand {
				t.Errorf("第三行应给可执行命令 %q，实际 %q", c.wantCommand, lines[2])
			}
			if prev, dup := advices[advice]; dup {
				t.Errorf("建议与用例 %q 重复（每类错误一种说法）：%q", prev, advice)
			}
			advices[advice] = c.name
		})
	}
}

// TestReportErrorFallbackKeepsUpgradeHint：固定句只在无法归类时保留；给不出可执行命令时
// 第三行必须整行省略，不留空行。
func TestReportErrorFallbackKeepsUpgradeHint(t *testing.T) {
	lines := reportLines(errors.New("混流失败: ffmpeg 退出码 1"))
	if len(lines) != 2 {
		t.Fatalf("归不了类且给不出命令时应只有两行，实际 %d 行：%q", len(lines), lines)
	}
	if want := "提示：请尝试升级到最新版本后重试!"; lines[1] != want {
		t.Errorf("无法归类时应保留上游固定句 %q，实际 %q", want, lines[1])
	}
}

// 参数解析失败（cobra 的未知标志/参数个数不对）归入「参数」类：建议 + 命令，其后照旧附 usage
// （上游 Spectre 在解析失败时打印帮助文本）。
func TestReportUsageErrorPrintsAdviceThenUsage(t *testing.T) {
	lines := reportLines(usageError{errors.New("unknown flag: --nope")})
	out := strings.Join(lines, "\n")
	if !strings.Contains(lines[0], "unknown flag: --nope") {
		t.Errorf("用法错误必须打印原因：%q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "提示：") || !strings.Contains(lines[1], "参数") {
		t.Errorf("用法错误应给「参数」类建议：%q", lines[1])
	}
	if want := "命令：bbdown --help"; lines[2] != want {
		t.Errorf("用法错误应给命令 %q，实际 %q", want, lines[2])
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("用法错误必须附带 usage（上游解析失败时给帮助文本）：%q", out)
	}
	if strings.Contains(out, "请尝试升级到最新版本后重试!") {
		t.Errorf("用法错误不该提示升级：%q", out)
	}
}

// 运行期错误绝不打印 usage：reportError 若对运行期错误也走 usage 分支，本用例变红。
func TestReportRuntimeErrorPrintsNoUsage(t *testing.T) {
	out := strings.Join(reportLines(errors.New("解析链接失败: 输入有误：无法识别的视频 URL 或 ID")), "\n")
	if strings.Contains(out, "Usage:") || strings.Contains(out, "--audio-only") {
		t.Errorf("运行期错误不应打印 usage/帮助：%q", out)
	}
}

// TestUnknownFlagIsClassifiedAsUsage 同时钉两件事：
// 未知标志被归类成用法错误（要跟着 usage），以及 cobra 自己不再抢着打印——
// 后者是 SilenceUsage/SilenceErrors 的守卫：撤掉它们，cobra 会先打出
// "Error: unknown flag: ..." 与整篇 usage，本用例变红。
func TestUnknownFlagIsClassifiedAsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"--definitely-not-a-flag"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("未知标志应当报错")
	}
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("未知标志必须归类为用法错误，实际 %T: %v", err, err)
	}
	if s := out.String() + errOut.String(); s != "" {
		t.Errorf("cobra 不应再自行打印（由 Execute/reportError 统一接管）：%q", s)
	}
}

// 参数个数不对（MinimumNArgs）与标志解析失败同属用法错误，要跟着 usage。
func TestMissingArgsIsClassifiedAsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"sub", "add"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("sub add 缺参数应当报错")
	}
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("缺参数必须归类为用法错误，实际 %T: %v", err, err)
	}
}
