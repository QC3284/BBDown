package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件搬上游 ConfigMergeTests：命令行显式给出的选项必须压过配置文件——
// 无论空格还是等号写法；配置文件里以 '-' 开头的取值不能被吞掉。

func TestUpstreamConfigMerge(t *testing.T) {
	aliasMap := map[string]string{
		"--dfn-priority": "dfn", "-q": "dfn",
		"--access-token": "token",
		"--config-file":  "configfile",
		"--debug":        "debug",
	}
	boolFlags := map[string]bool{"dfn": false, "token": false, "configfile": false, "debug": true}

	writeConfig := func(content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "BBDown.config")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// effective 取合并结果里某个长选项最终生效的值（后者胜出，与 Spectre 一致）。
	effective := func(merged []string, long string) string {
		val := ""
		for i := 0; i < len(merged); i++ {
			if merged[i] == long && i+1 < len(merged) {
				val = merged[i+1]
			} else if strings.HasPrefix(merged[i], long+"=") {
				val = merged[i][len(long)+1:]
			}
		}
		return val
	}

	// 1) 空格写法：命令行压过配置文件
	cfg := writeConfig("--dfn-priority\n1080P 高清\n")
	merged, err := MergeWithConfig([]string{"--dfn-priority", "720P 高清", "--config-file", cfg, "URL"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(merged, "--dfn-priority"); got != "720P 高清" {
		t.Errorf("空格写法：生效值 %q，上游期望 720P 高清", got)
	}

	// 2) 等号写法：--dfn-priority=X 同样必须被认作「已显式指定」
	cfg = writeConfig("--dfn-priority\n1080P 高清\n")
	merged, err = MergeWithConfig([]string{"--dfn-priority=720P 高清", "--config-file", cfg, "URL"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(merged, "--dfn-priority"); got != "720P 高清" {
		t.Errorf("等号写法：生效值 %q，上游期望 720P 高清（配置值不能被追加到末尾反压命令行）", got)
	}

	// 3) --config-file=<path> 形式必须被认（否则回落到默认配置路径，用户指定的文件被忽略）
	cfg = writeConfig("--dfn-priority\n1080P 高清\n")
	merged, err = MergeWithConfig([]string{"--config-file=" + cfg, "URL"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(merged, "--dfn-priority"); got != "1080P 高清" {
		t.Errorf("等号形式的 --config-file 未生效：%q", got)
	}

	// 4) 命令行没给的选项，取配置文件的值
	cfg = writeConfig("--dfn-priority\n1080P 高清\n")
	merged, err = MergeWithConfig([]string{"--config-file", cfg, "URL"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(merged, "--dfn-priority"); got != "1080P 高清" {
		t.Errorf("配置文件的值未生效：%q", got)
	}

	// 5) 配置文件里以 '-' 开头的取值不能被当成新选项吞掉
	cfg = writeConfig("--access-token\n-access-token-value\n")
	merged, err = MergeWithConfig([]string{"--config-file", cfg, "URL"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(merged, "--access-token"); got != "-access-token-value" {
		t.Errorf("以 - 开头的取值被吞：%q", got)
	}

	// 6) 子命令调用整段跳过配置合并（原样返回）
	cfg = writeConfig("--dfn-priority\n1080P 高清\n")
	cli := []string{"--config-file", cfg, "sub", "list"}
	merged, err = MergeWithConfig(cli, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(merged, " ") != strings.Join(cli, " ") {
		t.Errorf("子命令调用不应合并配置：%v", merged)
	}
	// 另两条判定由既有 TestIsSubCommandInvocation / TestMergeWithConfigKeepsURLWhenOptionValueLooksLikeURL 覆盖。
}
