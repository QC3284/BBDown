package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestWorkDirShorthandMatchesUpstream 钉住 -w 简写的分布：
// 上游在 ArticleSettings / LiveSettings / WatchLaterSettings 里声明 -w|--work-dir，
// 根命令的 --work-dir 则没有简写。少了 article/live 的 -w，
// `BBDown article -w <dir>` 会报 unknown shorthand flag（上游可用）。
func TestWorkDirShorthandMatchesUpstream(t *testing.T) {
	for _, c := range []struct {
		name string
		want string
	}{
		{"article", "w"},
		{"live", "w"},
		{"watchlater", "w"},
	} {
		cmd := findCommand(c.name)
		if cmd == nil {
			t.Fatalf("找不到子命令 %s", c.name)
		}
		f := cmd.Flags().Lookup("work-dir")
		if f == nil {
			t.Errorf("%s 缺 --work-dir", c.name)
			continue
		}
		if f.Shorthand != c.want {
			t.Errorf("%s 的 --work-dir 简写为 %q，上游为 %q", c.name, f.Shorthand, c.want)
		}
	}

	// 根命令的 --work-dir 不应有简写（上游 MyOption 如此）
	if f := rootCmd.PersistentFlags().Lookup("work-dir"); f == nil || f.Shorthand != "" {
		t.Errorf("根命令 --work-dir 不应有简写，实际 %v", f)
	}
}

// findCommand 在根命令的子命令树里按名字找命令。
func findCommand(name string) *cobra.Command {
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return c
		}
		if sub := findCommandIn(c, name); sub != nil {
			return sub
		}
	}
	return nil
}

func findCommandIn(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
