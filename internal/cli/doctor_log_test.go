package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
)

// doctor 的输出统一：人类可读形态走 util 日志（终端与 --log-file 同时覆盖），JSON 形态保持机读。

// stubDoctorChecks 把自检项换成固定的两条（离线、可预期）：一条 ok、一条 fail；
// 用例结束自动还原。
func stubDoctorChecks(t *testing.T) {
	t.Helper()
	orig := doctorChecks
	doctorChecks = []func(context.Context, config.MyOption, *util.HTTPClient) doctorResult{
		func(context.Context, config.MyOption, *util.HTTPClient) doctorResult {
			return doctorResult{"假工具", "ok", "桩：一切正常"}
		},
		func(context.Context, config.MyOption, *util.HTTPClient) doctorResult {
			return doctorResult{"假接口", "fail", "桩：被风控拦截(HTTP 412)"}
		},
	}
	t.Cleanup(func() { doctorChecks = orig })
}

// TestDoctorOutputGoesToConsoleAndLogFile：自检结果必须**同时**出现在终端与 --log-file 里。
// 改前 doctor 直接 fmt.Fprintf(os.Stdout, ...) 绕过 logger：用户带 --log-file 跑完 doctor 提 issue 时，
// 日志文件里恰恰缺了 [fail] 那几行——最该留下的东西没有留下。
//
// 变异验证：把 runDoctor 的 util.Log/util.LogError 换回 fmt.Fprintf(os.Stdout, ...)，
// 日志文件那一半断言变红（文件不会被写出/为空）。
func TestDoctorOutputGoesToConsoleAndLogFile(t *testing.T) {
	stubDoctorChecks(t)

	logPath := filepath.Join(t.TempDir(), "doctor.log")
	util.SetLogFile(logPath)
	t.Cleanup(func() { util.SetLogFile("") })

	var code int
	console := captureStdout(t, func() {
		code = runDoctor(context.Background(), config.DefaultMyOption(), nil)
	})
	if code != 1 {
		t.Fatalf("有 fail 项时退出码应为 1，实际 %d（控制台输出 %q）", code, console)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("自检输出必须落进日志文件：%v", err)
	}
	logged := string(body)

	for _, want := range []string{"[ok]", "假工具", "桩：一切正常", "[fail]", "假接口", "桩：被风控拦截(HTTP 412)", "存在阻塞性问题"} {
		if !strings.Contains(console, want) {
			t.Errorf("终端输出缺少 %q：%q", want, console)
		}
		if !strings.Contains(logged, want) {
			t.Errorf("--log-file 里缺少 %q（doctor 绕过了 logger）：%q", want, logged)
		}
	}
}

// TestDoctorJSONStaysMachineReadable：JSON 分支保持机读契约——写 cmd 的输出流、纯 JSON、
// 不带日志时间戳与色码（否则 bbdown doctor --json | jq 直接失败）。
//
// 变异验证：把 runDoctorJSON 改成走 util.Log（带时间戳）→ Unmarshal 失败，本用例变红。
func TestDoctorJSONStaysMachineReadable(t *testing.T) {
	stubDoctorChecks(t)
	if err := doctorCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = doctorCmd.Flags().Set("json", "false") })

	var buf bytes.Buffer
	doctorCmd.SetOut(&buf)
	t.Cleanup(func() { doctorCmd.SetOut(nil) })

	if err := doctorCmd.RunE(doctorCmd, nil); err == nil {
		t.Error("有 fail 项时 doctor --json 应返回错误（脚本据此判读退出码）")
	}

	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("--json 的输出必须是纯 JSON（不能被日志时间戳/色码污染）：%v\n%q", err, buf.String())
	}
	if len(got) != 2 || got[0]["level"] != "ok" || got[1]["level"] != "fail" {
		t.Errorf("JSON 内容不符：%v", got)
	}
}
