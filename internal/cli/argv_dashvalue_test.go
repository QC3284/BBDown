package cli

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// t48：argv 里以 `-` 开头的**值**（以及 BBDown.config 里的同类值）的核查网。
// 结论与理由写在 root.go 的注释处（上游 1.6.22 对齐核查）。
const argvProbeEnv = "BBDOWN_TEST_ARGV_PROBE"

// TestCLIArgvProbe 是**真实 CLI 子进程**探针：只在子进程里运行，把 `--` 之后的真实 argv
// 走 Execute 同一条预处理链，再用真实 rootCmd 解析，结果打成 JSON 供父用例断言。
func TestCLIArgvProbe(t *testing.T) {
	if os.Getenv(argvProbeEnv) != "1" {
		t.Skip("argv 探针：仅在作为子进程被调用时运行（父用例设 " + argvProbeEnv + "=1 并用 `--` 传 CLI 参数）")
	}
	if err := json.NewEncoder(os.Stdout).Encode(probeArgv(flag.Args())); err != nil {
		t.Fatal(err)
	}
}

// probeArgv 走 Execute 的同一条链路（normalizeCliArgsSkippingValues → foldBoolFlagValues → mergeConfigArgs
// → 再折一次）后用真实 rootCmd 解析，返回关心的标志值与解析错误。
func probeArgv(cliArgs []string) map[string]any {
	// 走**生产路径同一份实现**（preprocessArgs），不在用例里另抄一条链：生产代码改了，
	// 探针与断言会一起红。
	effective, configLoaded, mergeErr := preprocessArgs(cliArgs)
	out := map[string]any{
		"argv":      cliArgs,
		"config":    configLoaded,
		"merge_err": errText(mergeErr),
		"effective": effective,
		"parse_err": "",
	}
	rootCmd.SetArgs(effective)
	err := rootCmd.ParseFlags(effective)
	out["parse_err"] = errText(err)
	for _, name := range []string{"work-dir", "user-agent", "danmaku-filter", "skip-mux", "skip-subtitle", "multi-thread", "config-file", "version"} {
		f := rootCmd.Flags().Lookup(name)
		if f == nil {
			f = rootCmd.PersistentFlags().Lookup(name)
		}
		if f != nil {
			out[name] = f.Value.String()
			out[name+"_changed"] = f.Changed
		}
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// runArgvProbe 起一个真实子进程（测试二进制自身）跑探针。CLI 参数放在 `--` 之后：Go 的
// flag 包把 `--` 之后原样留在 flag.Args()，所以子进程拿到的是**真正的进程 argv**。
func runArgvProbe(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestCLIArgvProbe", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), argvProbeEnv+"=1")
	cmd.Dir = t.TempDir() // 固定工作目录：不读调用方 cwd 下的 BBDown.config
	run, err := cmd.Output()
	if err != nil {
		t.Fatalf("探针子进程失败: %v\n%s", err, run)
	}
	// 探针只打一行 JSON；这之后是 testing 自己的 "PASS" 行，取首行即可。
	line, _, _ := strings.Cut(string(run), "\n")
	var res map[string]any
	if uerr := json.Unmarshal([]byte(line), &res); uerr != nil {
		t.Fatalf("探针输出不是 JSON: %v\n%s", uerr, run)
	}
	return res
}

// TestDashPrefixedValuesInArgv 是 t48 的**防回归网**（真实 CLI 子进程）：`-` 开头的值必须被逐字
// 取用，且空格写法与 `=` 写法结果一致。pflag 这条链路实测本就正确（结论与实测矩阵见 root.go 的
// 注释），本用例把它钉住——防止将来有人在预处理链里「顺手」把 `-` 开头的值当成选项（那正是
// 上游 1.6.22 修的 C# tokenizer 行为）。
//
// 变异验证：（若有人把值当选项）在 foldBoolFlagValues / normalizeCliArgs 链里对 `-` 开头的 token
// 一律当选项处理 → 本用例的对应断言立刻红；本轮的**实际修复点**（别名重写跳过值位置）由
// TestDashAliasesInValuePositionAreNotRewritten 钉住。
func TestDashPrefixedValuesInArgv(t *testing.T) {
	cases := []struct {
		name string
		flag string
		want string
	}{
		{"work-dir 空格写法", "work-dir", "-x"},
		{"work-dir = 写法", "work-dir", "-x"},
		{"user-agent 短横线开头的值（空格）", "user-agent", "-尾野"},
		{"user-agent 短横线开头的值（=）", "user-agent", "-尾野"},
		{"danmaku-filter -abc（空格）", "danmaku-filter", "-abc"},
		{"danmaku-filter -abc（=）", "danmaku-filter", "-abc"},
		{"已知选项名也能当值（GNU 语义，不特殊处理）", "work-dir", "--cookie"},
	}
	argv := map[string][]string{
		"work-dir 空格写法":           {"--work-dir", "-x"},
		"work-dir = 写法":           {"--work-dir=-x"},
		"user-agent 短横线开头的值（空格）":  {"--user-agent", "-尾野"},
		"user-agent 短横线开头的值（=）":   {"--user-agent=-尾野"},
		"danmaku-filter -abc（空格）": {"--danmaku-filter", "-abc"},
		"danmaku-filter -abc（=）":  {"--danmaku-filter=-abc"},
		"已知选项名也能当值（GNU 语义，不特殊处理）": {"--work-dir", "--cookie"},
	}
	got := map[string]string{}
	for _, tc := range cases {
		res := runArgvProbe(t, argv[tc.name]...)
		if err := str(res["parse_err"]); err != "" {
			t.Errorf("%s：解析不该失败（`-` 开头的值是合法值），实际 %s（effective=%v）", tc.name, err, res["effective"])
		}
		if v := str(res[tc.flag]); v != tc.want {
			t.Errorf("%s：%s = %q，want %q（effective=%v）", tc.name, tc.flag, v, tc.want, res["effective"])
		}
		got[tc.name] = str(res[tc.flag])
	}
	// 空格写法与 = 写法必须**逐字一致**（不是各自「看起来对」）。
	for _, pair := range [][2]string{
		{"work-dir 空格写法", "work-dir = 写法"},
		{"user-agent 短横线开头的值（空格）", "user-agent 短横线开头的值（=）"},
		{"danmaku-filter -abc（空格）", "danmaku-filter -abc（=）"},
	} {
		if got[pair[0]] != got[pair[1]] {
			t.Errorf("两种写法结果不一致：%s=%q vs %s=%q", pair[0], got[pair[0]], pair[1], got[pair[1]])
		}
	}
}

// TestBoolSwitchDoesNotSwallowNextOption 钉住 bool 开关的边界：它不取值，所以后面的选项/
// 值都不能被它吞掉；同时上游 arity 0..1 的 `--multi-thread false` 折行写法仍然有效。
func TestBoolSwitchDoesNotSwallowNextOption(t *testing.T) {
	// 两个 bool 开关连着写：都置真（不误合并）。
	res := runArgvProbe(t, "--skip-mux", "--skip-subtitle")
	if str(res["parse_err"]) != "" {
		t.Fatalf("--skip-mux --skip-subtitle 必须合法，实际 %s", str(res["parse_err"]))
	}
	for _, f := range []string{"skip-mux", "skip-subtitle"} {
		if v := str(res[f]); v != "true" {
			t.Errorf("%s = %q，want true（bool 连着写不该丢）", f, v)
		}
	}

	// bool 后紧跟「取值选项 + 以 - 开头的值」：三者都要正确落地。
	res = runArgvProbe(t, "--skip-mux", "--danmaku-filter", "-abc")
	if str(res["skip-mux"]) != "true" || str(res["danmaku-filter"]) != "-abc" {
		t.Errorf("bool 后跟取值选项被误合并：skip-mux=%q danmaku-filter=%q", str(res["skip-mux"]), str(res["danmaku-filter"]))
	}

	// 上游 System.CommandLine 的 arity 0..1 写法（bool 后跟 true/false）仍要折平成 = 形式。
	res = runArgvProbe(t, "--multi-thread", "false")
	if v := str(res["multi-thread"]); v != "false" {
		t.Errorf("上游写法 --multi-thread false 失效：multi-thread = %q（effective=%v）", v, res["effective"])
	}

	// 负例：未知短选项必须报错，而不是被前一个 bool 开关吞成它的值。
	res = runArgvProbe(t, "--skip-mux", "-Ztoken")
	if msg := str(res["parse_err"]); !strings.Contains(msg, "unknown shorthand flag") {
		t.Errorf("未知短选项应当报 unknown shorthand flag，实际 %q（effective=%v）", msg, res["effective"])
	}
}

// TestDashAliasesInValuePositionAreNotRewritten 钉住本轮**唯一的修复点**：
// `-help` / `-?` / `-version` 的别名重写只做在**选项位置**，值位置不能动——
// 否则 `--user-agent -version` 会变成 `--user-agent --version`（UA 被静默写成 "--version"，
// 不报错但错），这正是「`-` 开头的值被当成选项」这一类问题的同一族。
//
// 变异验证：把 Execute 里的 normalizeCliArgsSkippingValues 换回 normalizeCliArgs → 本用例红。
func TestDashAliasesInValuePositionAreNotRewritten(t *testing.T) {
	for _, v := range []string{"-version", "-help", "-?"} {
		res := runArgvProbe(t, "--user-agent", v)
		if got := str(res["user-agent"]); got != v {
			t.Errorf("值位置的 %q 被别名重写成了 %q（effective=%v）", v, got, res["effective"])
		}
		if err := str(res["parse_err"]); err != "" {
			t.Errorf("值位置的 %q 不该导致解析失败：%s", v, err)
		}
		// = 写法本来就自带值，同样必须逐字保留。
		if got := str(runArgvProbe(t, "--user-agent="+v)["user-agent"]); got != v {
			t.Errorf("--user-agent=%s 的值被改成了 %q", v, got)
		}
	}

	// 反向：站在选项位置上的这三个别名仍要重写（别把修复做过头）。
	for _, tc := range []struct{ in, want string }{
		{"-version", "--version"},
		{"-help", "--help"},
		{"-?", "--help"},
	} {
		effective := strs(runArgvProbe(t, tc.in)["effective"])
		if len(effective) != 1 || effective[0] != tc.want {
			t.Errorf("选项位置的 %s 应重写为 %s，实际 effective=%v", tc.in, tc.want, effective)
		}
	}
}

// TestConfigFileDashPrefixedValues：BBDown.config 侧同测——配置行按「第一个空格」拆成
// 选项 + 值，值以 `-` 开头时必须逐字生效（同样用真实 CLI 子进程 + --config-file 验证）。
func TestConfigFileDashPrefixedValues(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "BBDown.config")
	content := "--work-dir -x\n--danmaku-filter -abc\n--user-agent -尾野\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runArgvProbe(t, "--config-file", cfg)
	if str(res["merge_err"]) != "" {
		t.Fatalf("配置合并不该失败：%s", str(res["merge_err"]))
	}
	if !truthy(res["config"]) {
		t.Errorf("配置文件应被加载（config=%v）", res["config"])
	}
	for flag, want := range map[string]string{"work-dir": "-x", "danmaku-filter": "-abc", "user-agent": "-尾野"} {
		if got := str(res[flag]); got != want {
			t.Errorf("配置里的 %s = %q，want %q（effective=%v）", flag, got, want, res["effective"])
		}
	}
}

// str / strs / truthy 是探针 JSON 的取值助手（缺键、类型不符都按零值处理，用例自己断言）。
func str(v any) string {
	s, _ := v.(string)
	return s
}

func strs(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, str(item))
	}
	return out
}

func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}
