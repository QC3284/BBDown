package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
)

// 任务 M 的守卫用例：机读模式（--info-json / doctor --json）下 stdout 只留数据，
// 日志让位到 stderr；而且让位是**本命令作用域**的——命令退出必须还原成 os.Stdout，
// 否则同包后续用例「替换 os.Stdout 捕获输出」的手法就再也拿不到日志。
//
// 上一次尝试（f828cba，已 revert）就是栽在这两条上：目标被冻结在变量里 → 捕获失效；
// 全局可变目标不还原 → 跨用例泄漏。两条用例各守一半。
//
// 变异验证（四处都实测过，都是断言红而不是构建错误）：
//   - RedirectConsoleLogs 改成空操作（机制撤掉）→ 两条 RunE 用例的「stdout 纯净」断言变红；
//   - RedirectConsoleLogs 不还原（全局目标泄漏）→ 末尾「捕获哨兵」断言变红，正是 f828cba 的
//     「后续用例捕获到空串」形态；
//   - yieldLogsToStderr 改成空操作 → 两条 RunE 用例变红（RunE 里的安装是承重的）；
//   - machineReadableArgs 恒 false → TestMachineReadableArgs 变红（横幅/让位的判定）。

// TestMachineReadableArgs 钉住机读模式的判定：只有 --info-json 与 doctor --json 算机读
// （后写覆盖先写，与 pflag 一致）；人看的形态一律不算——判宽了会误省横幅、误让位日志。
//
// 变异验证：判定改成恒 true → 第二组（人看形态）变红；改成恒 false → 第一组变红。
func TestMachineReadableArgs(t *testing.T) {
	yes := [][]string{
		{"--info-json", "BV1"},
		{"BV1", "--info-json"},
		{"--info-json=true", "BV1"},
		{"--info-json=false", "--info-json", "BV1"}, // 后写覆盖先写
		{"doctor", "--json"},
		{"--json", "doctor"},
		{"doctor", "--json=true"},
	}
	for _, args := range yes {
		if !machineReadableArgs(args) {
			t.Errorf("%v 应当判定为机读模式（stdout 只留数据）", args)
		}
	}
	no := [][]string{
		{"BV1"},
		{"-I", "BV1"},
		{"--print-urls", "BV1"},
		{"doctor"},
		{"doctor", "--json=false"},
		{"--json=false", "doctor"},
		{"doctor", "--json", "--json=false"}, // 后写覆盖先写
		{"--info-json=false", "BV1"},
		{"--json", "BV1"}, // 没有 doctor 子命令：--json 不是根命令的开关
	}
	for _, args := range no {
		if machineReadableArgs(args) {
			t.Errorf("%v 不该判定为机读模式（人看的输出要保持原样）", args)
		}
	}
}

// TestPrepareConsoleOutputKeepsHumanModeIntact：人看的模式一字不改——横幅照打、日志照旧走
// os.Stdout；只有机读模式才不打横幅、把日志让位到 stderr。
//
// 变异验证：让位改成无条件进行 → 人看那一半的横幅断言变红；去掉让位实现 → 机读那一半变红。
func TestPrepareConsoleOutputKeepsHumanModeIntact(t *testing.T) {
	const banner = "BANNER\n"

	var restore func()
	stdout, stderr := captureStdoutStderr(t, func() {
		restore = prepareConsoleOutput(false, banner)
		util.Log("人看模式日志 %d", 1)
	})
	restore()
	if !strings.HasPrefix(stdout, banner) {
		t.Errorf("人看模式必须先打横幅，实际 stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "人看模式日志 1") {
		t.Errorf("人看模式的日志仍应走 stdout，实际 stdout=%q", stdout)
	}
	if stderr != "" {
		t.Errorf("人看模式的 stderr 不该有输出，实际 %q", stderr)
	}

	stdout, stderr = captureStdoutStderr(t, func() {
		restore = prepareConsoleOutput(true, banner)
		util.Log("机读模式日志 %d", 2)
	})
	restore()
	if stdout != "" {
		t.Errorf("机读模式不打横幅、日志也不该留在 stdout，实际 %q", stdout)
	}
	if !strings.Contains(stderr, "机读模式日志 2") {
		t.Errorf("机读模式的日志应让位到 stderr，实际 stderr=%q", stderr)
	}
	assertStdoutCaptureStillWorks(t)
}

// captureStdoutStderr 同时替换 os.Stdout / os.Stderr，收集 fn 期间两边的输出。
func captureStdoutStderr(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	outCh, errCh := make(chan string, 1), make(chan string, 1)
	go func() { b, _ := io.ReadAll(outR); outCh <- string(b) }()
	go func() { b, _ := io.ReadAll(errR); errCh <- string(b) }()

	fn()

	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outW.Close()
	_ = errW.Close()
	stdout, stderr = <-outCh, <-errCh
	_ = outR.Close()
	_ = errR.Close()
	return stdout, stderr
}

// assertStdoutCaptureStillWorks 是守卫的后半条：跑完机读命令后，「替换 os.Stdout 捕获输出」
// 必须照旧有效——让位没还原的话这里只能捕获到空串。
func assertStdoutCaptureStillWorks(t *testing.T) {
	t.Helper()
	sentinel := captureStdout(t, func() { util.Log("让位还原哨兵 %d", 42) })
	if !strings.Contains(sentinel, "让位还原哨兵 42") {
		t.Errorf("跑完机读命令后 os.Stdout 捕获失效（日志让位没有还原）：捕获到 %q", sentinel)
	}
}

// TestDoctorJSONKeepsStdoutPureAndRestoresLogs：doctor --json 走**真实 RunE**：
// stdout 整段必须是 JSON（混进一条带时间戳的日志，管道消费就失败），日志让位到 stderr，
// 且退出后 os.Stdout 捕获恢复。
func TestDoctorJSONKeepsStdoutPureAndRestoresLogs(t *testing.T) {
	orig := doctorChecks
	doctorChecks = []func(context.Context, config.MyOption, *util.HTTPClient) doctorResult{
		func(context.Context, config.MyOption, *util.HTTPClient) doctorResult {
			util.Log("自检桩日志 %d", 1) // 让位探测点：机读模式下它必须走 stderr
			return doctorResult{"假工具", "ok", "桩：一切正常"}
		},
	}
	t.Cleanup(func() { doctorChecks = orig })

	if err := doctorCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = doctorCmd.Flags().Set("json", "false") })

	var runErr error
	stdout, stderr := captureStdoutStderr(t, func() { runErr = doctorCmd.RunE(doctorCmd, nil) })
	if runErr != nil {
		t.Fatalf("全部 ok 时 doctor --json 不该报错：%v（stdout=%q stderr=%q）", runErr, stdout, stderr)
	}

	var got []map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("doctor --json 的 stdout 必须整段是 JSON：%v\nstdout=%q\nstderr=%q", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "自检桩日志 1") {
		t.Errorf("机读模式下日志应当让位到 stderr（不是被丢掉），实际 stderr=%q", stderr)
	}
	assertStdoutCaptureStillWorks(t)
}

// TestInfoJSONKeepsStdoutPureAndRestoresLogs：--info-json 同理，走根命令的**真实 RunE**。
// 目标用无法识别的地址：解析在本地失败、不碰网络，但 RunE 里已经打出了警告与收尾汇总两行日志，
// 正好当让位探测点。
func TestInfoJSONKeepsStdoutPureAndRestoresLogs(t *testing.T) {
	t.Chdir(t.TempDir())   // 失败目标会写「未完成任务」清单：别落到仓库里
	installInterruptSpy(t) // 下载路径会取中断 ctx：换成假实现，不真注册信号

	origCheck := updateCheck
	updateCheck = func(context.Context, *util.HTTPClient, string) {} // 用例离网：更新检查是 RunE 里唯一的后台网络调用
	t.Cleanup(func() { updateCheck = origCheck })

	origInfo, origApp, origToken := optInfoJSON, optUseAppAPI, optToken
	optInfoJSON, optUseAppAPI, optToken = true, true, ""
	t.Cleanup(func() { optInfoJSON, optUseAppAPI, optToken = origInfo, origApp, origToken })

	var runErr error
	stdout, stderr := captureStdoutStderr(t, func() {
		runErr = rootCmd.RunE(rootCmd, []string{"这不是一个视频地址"})
	})
	if runErr == nil {
		t.Fatal("无法识别的目标应当失败")
	}

	// ① 本次解析失败没有 JSON 产物：stdout 应当一个字节的日志都没有。
	if stdout != "" {
		t.Errorf("--info-json 的 stdout 只该有数据，实际混进了 %q", stdout)
	}
	// ② 两处日志都必须让位到 stderr。
	for _, want := range []string{"提示: APP 接口", "⚠ 下载完成   成功 0 · 失败 1 · "} {
		if !strings.Contains(stderr, want) {
			t.Errorf("让位后的日志缺少 %q，stderr=%q", want, stderr)
		}
	}
	assertStdoutCaptureStillWorks(t)
}
