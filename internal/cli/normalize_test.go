package cli

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

func TestNormalizeCliArgs(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"-help"}, []string{"--help"}},
		{[]string{"-?"}, []string{"--help"}},
		{[]string{"-version"}, []string{"--version"}},
		{[]string{"-I", "av170001"}, []string{"-I", "av170001"}},
		{[]string{"--help"}, []string{"--help"}},
	}
	for _, c := range cases {
		got := normalizeCliArgs(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("normalizeCliArgs(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestFoldBoolFlagValues 钉住上游 System.CommandLine 的布尔选项写法：
// 它的 arity 是 0..1，上游 README 就按 `--multi-thread false` 文档化；pflag 只认
// `--flag=false`，直接吃 `--flag false` 会把 "false" 留成位置参数并当成输入 URL。
func TestFoldBoolFlagValues(t *testing.T) {
	aliasMap := map[string]string{
		"--multi-thread": "multi-thread",
		"-m":             "multi-thread",
		"--interactive":  "interactive",
		"-i":             "interactive",
		"--cookie":       "cookie",
		"-c":             "cookie",
	}
	boolFlags := map[string]bool{"multi-thread": true, "interactive": true, "cookie": false}

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"长选项折行", []string{"--multi-thread", "false"}, []string{"--multi-thread=false"}},
		{"短选项同样折行（别名表由上层给出）", []string{"-m", "false"}, []string{"-m=false"}},
		{"true 同样折", []string{"--multi-thread", "true"}, []string{"--multi-thread=true"}},
		{"大小写不敏感", []string{"--multi-thread", "FALSE"}, []string{"--multi-thread=false"}},
		{"已带等号不动", []string{"--multi-thread=false"}, []string{"--multi-thread=false"}},
		{"无值不动", []string{"--multi-thread", "av170001"}, []string{"--multi-thread", "av170001"}},
		{"末尾无值不动", []string{"av170001", "--interactive"}, []string{"av170001", "--interactive"}},
		{"字符串选项的值不能吃", []string{"--cookie", "false"}, []string{"--cookie", "false"}},
		{"位置参数照旧", []string{"av170001", "--interactive", "false"}, []string{"av170001", "--interactive=false"}},
		{"-- 之后是位置参数", []string{"--", "--multi-thread", "false"}, []string{"--", "--multi-thread", "false"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := foldBoolFlagValues(c.in, aliasMap, boolFlags)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("foldBoolFlagValues(%v) = %v, 期望 %v", c.in, got, c.want)
			}
		})
	}
}

// TestFoldBoolFlagValuesFeedsPflag 用真实 pflag 验证折行的目的：
// 折行前 `--multi-thread false` 会同时「置 true + 多出一个位置参数」，折行后才是关闭。
func TestFoldBoolFlagValuesFeedsPflag(t *testing.T) {
	aliasMap := map[string]string{"--multi-thread": "multi-thread", "-m": "multi-thread"}
	boolFlags := map[string]bool{"multi-thread": true}

	parse := func(in []string) (bool, int, error) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		v := fs.BoolP("multi-thread", "m", true, "")
		if err := fs.Parse(in); err != nil {
			return false, 0, err
		}
		return *v, fs.NArg(), nil
	}

	// 折行后：真的关闭，且没有多余位置参数。
	for _, in := range [][]string{{"--multi-thread", "false"}, {"-m", "false"}} {
		v, n, err := parse(foldBoolFlagValues(in, aliasMap, boolFlags))
		if err != nil {
			t.Fatalf("parse %v: %v", in, err)
		}
		if v || n != 0 {
			t.Errorf("%v 折行后 multi-thread=%v、剩余位置参数 %d，期望 false 与 0", in, v, n)
		}
	}

	// 折行前：正是要修的现象——置成 true 并漏出一个 "false" 位置参数。
	v, n, err := parse([]string{"--multi-thread", "false"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !v || n != 1 {
		t.Errorf("未折行时 multi-thread=%v、位置参数 %d，期望 true 与 1（这就是必须折行的原因）", v, n)
	}
}
