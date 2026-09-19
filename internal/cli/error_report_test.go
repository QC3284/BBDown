package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// 上游（Spectre.Console.Cli）的两种失败输出泾渭分明：
//   - 参数解析失败 → 错误 + 帮助文本；
//   - 运行期异常 → SetExceptionHandler：只有异常消息 + 一句升级提示，绝不打印帮助。
//
// cobra 默认对**任何** RunE 错误都打印 usage（还有自带的 "Error: " 前缀），
// 用户实测到的就是「非命令错误，执行失败时也会弹出帮助」。

func TestReportRuntimeErrorPrintsNoUsage(t *testing.T) {
	var buf bytes.Buffer
	reportError(errors.New("解析链接失败: 输入有误：无法识别的视频 URL 或 ID"), &buf)
	out := buf.String()
	if !strings.Contains(out, "解析链接失败") {
		t.Errorf("运行期错误必须打印消息本身：%q", out)
	}
	if strings.Contains(out, "Usage:") || strings.Contains(out, "--audio-only") {
		t.Errorf("运行期错误不应打印 usage/帮助：%q", out)
	}
	if !strings.Contains(out, "请尝试升级到最新版本后重试!") {
		t.Errorf("缺少上游 SetExceptionHandler 的升级提示：%q", out)
	}
}

func TestReportUsageErrorPrintsUsage(t *testing.T) {
	var buf bytes.Buffer
	reportError(usageError{errors.New("unknown flag: --nope")}, &buf)
	out := buf.String()
	if !strings.Contains(out, "unknown flag: --nope") {
		t.Errorf("用法错误必须打印原因：%q", out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("用法错误必须附带 usage（上游解析失败时给帮助文本）：%q", out)
	}
	if strings.Contains(out, "请尝试升级到最新版本后重试!") {
		t.Errorf("用法错误不该提示升级：%q", out)
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
