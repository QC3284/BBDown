package cli

import (
	"strings"
	"testing"
)

// 首屏体验守卫：--help 的示例区必须覆盖我们自己的特色能力——
// 新功能做完却不出现在帮助里，等于用户不知道它存在（本项目已经攒了 doctor/resume/--print-urls 等一批）。
//
// 变异验证：从 Long 里删掉任意一行示例（如 doctor）→ 本用例变红。
func TestRootHelpListsOwnFeatures(t *testing.T) {
	long := rootCmd.Long
	for _, want := range []string{
		"常用示例",
		"BBDown doctor",
		"BBDown resume",
		"--print-urls",
		"--urls-file",
		"--nfo",
		"--progress-json",
		"--overwrite",
		"--write-m3u",
		"--compat",
		"--info-json",
		"BBDown login",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("帮助里应当出现 %q（新功能要在首屏可见）", want)
		}
	}
	// 帮助里不该出现内部文档路径之外的东西；这里只确认它指向差异台账，便于用户查行为差异。
	if !strings.Contains(long, "UPSTREAM_ALIGNMENT.md") {
		t.Error("帮助应当指向差异台账（用户要能查到与上游的行为差异）")
	}
}

// 每个自研子命令都要在 help 里有一行说明（Short 非空）。
func TestOwnSubcommandsHaveShortHelp(t *testing.T) {
	for _, cmd := range []struct {
		name  string
		short string
	}{
		{"doctor", doctorCmd.Short},
		{"resume", resumeCmd.Short},
	} {
		if strings.TrimSpace(cmd.short) == "" {
			t.Errorf("子命令 %s 缺少 Short 说明", cmd.name)
		}
	}
}
