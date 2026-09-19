package cli

import "testing"

// 本文件搬上游 OptionDefaultsBindingTests：帮助文本标称「默认开启」的布尔选项，
// 实际默认值必须真的是 true——Spectre 当年会把未出现的 flag 写回 false，覆盖属性初始化器。

func TestUpstreamOptionDefaults(t *testing.T) {
	defaultOn := []string{"multi-thread", "skip-ai", "force-replace-host"}
	defaultOff := []string{"force-http", "use-tv-api", "only-show-info", "audio-only"}

	for _, name := range defaultOn {
		f := rootCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("缺少 --%s", name)
		}
		if f.DefValue != "true" {
			t.Errorf("--%s 帮助文本称默认开启，实际默认值 %q", name, f.DefValue)
		}
	}

	for _, name := range defaultOff {
		f := rootCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("缺少 --%s", name)
		}
		if f.DefValue != "false" {
			t.Errorf("--%s 应默认关闭，实际默认值 %q", name, f.DefValue)
		}
	}
}
