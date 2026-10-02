package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// 任务 R：aria2c 的逐字节进度。
//
// 解析器是纯函数（喂字符串、拿数值），接线用例用**假 aria2c**（POSIX shell 脚本）把摘要块写到
// stderr，验证它经既有观察者管线变成进度帧、且摘要行被消费、错误行被原样转发。
// 脚本形态的用例在 Windows 上 t.Skip（与 size_verify_test 同一约定）。

// skipWithoutPOSIXShell 统一两处跳过理由：假 aria2c 是 shell 脚本。
func skipWithoutPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake aria2c is a POSIX shell script")
	}
}

// TestParseAria2SummaryLine 钉住摘要行的字段解析：进度（已下载/总量）、速率、ETA。
//
// 判据里的数字都是**字节**：摘要写 20MiB 就是 20×1024×1024，速率同理（DL:6.2MiB → 字节/秒）。
//
// 变异验证：把 parseAria2Size 的二进制倍率改成十进制（1e6）→ 前两行变红；
// 把 parseAria2ETA 的单位乘数写错（m 当 100）→ 1m20s 那行变红。
func TestParseAria2SummaryLine(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name      string
		line      string
		wantOK    bool
		completed int64
		total     int64
		speed     float64
		eta       int64
	}{
		{
			"交付说明里的标准行",
			`[#abc123 20MiB/100MiB(20%) CN:3 DL:6.2MiB ETA:13s]`,
			true, 20 * mib, 100 * mib, 6.2 * mib, 13,
		},
		{
			"字段顺序无关：DL 在 CN 前",
			`[#a1 1MiB/2MiB(50%) DL:512KiB CN:1 ETA:2s]`,
			true, 1 * mib, 2 * mib, 512 * 1024, 2,
		},
		{
			"没有速率与 ETA 的行：照样给出进度（其余字段留 0）",
			`[#a1 30MiB/100MiB(30%)]`,
			true, 30 * mib, 100 * mib, 0, 0,
		},
		{
			"百分比与字节不一致时以**字节**为准（百分比是摘要里的整数近似）",
			`[#a1 25MiB/100MiB(20%)]`,
			true, 25 * mib, 100 * mib, 0, 0,
		},
		{
			"十进制单位也认（不同版本/平台写法）",
			`[#a1 1.5MB/10MB(15%) DL:1MB ETA:8s]`,
			true, 1500000, 10000000, 1000000, 8,
		},
		{
			"裸字节与小体积",
			`[#a1 512B/2KiB(25%) DL:64B ETA:24s]`,
			true, 512, 2048, 64, 24,
		},
		{
			"h/m/s 复合 ETA",
			`[#a1 1GiB/2GiB(50%) DL:8MiB ETA:2h3m4s]`,
			true, 1 << 30, 2 << 30, 8 * mib, 2*3600 + 3*60 + 4,
		},
		{
			"行尾 \\r 与两侧空白都容忍（aria2 的就地重绘会带 CR）",
			"  [#a1 4MiB/8MiB(50%) ETA:1m20s]\r",
			true, 4 * mib, 8 * mib, 0, 80,
		},
		// ---- 畸形行：一律 false + 零值，绝不 panic ----
		{"空行", "", false, 0, 0, 0, 0},
		{"只有空白", "   ", false, 0, 0, 0, 0},
		{"不是方括号行", "Download complete: out.bin", false, 0, 0, 0, 0},
		{"只有一侧括号", "[#a1 1MiB/2MiB", false, 0, 0, 0, 0},
		{"空方括号", "[]", false, 0, 0, 0, 0},
		{"没有 gid", "[a1 1MiB/2MiB(50%)]", false, 0, 0, 0, 0},
		{"没有进度字段", `[#a1 CN:3]`, false, 0, 0, 0, 0},
		{"进度里没有斜杠", `[#a1 20MiB(20%)]`, false, 0, 0, 0, 0},
		{"体积不是数字", `[#a1 abc/def(1%)]`, false, 0, 0, 0, 0},
		{"总量为 0", `[#a1 20MiB/0B(0%)]`, false, 0, 0, 0, 0},
		{"总量未知（?）", `[#a1 20MiB/?(1%)]`, false, 0, 0, 0, 0},
		{"负数字节", `[#a1 -1MiB/100MiB(0%)]`, false, 0, 0, 0, 0},
		{"速率畸形：不影响进度字段，速率留 0", `[#a1 20MiB/100MiB(20%) DL:xyz ETA:??]`,
			true, 20 * mib, 100 * mib, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseAria2SummaryLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("parseAria2SummaryLine(%q) ok = %v，期望 %v（got %+v）", tc.line, ok, tc.wantOK, got)
			}
			if !ok {
				if got != (aria2Summary{}) {
					t.Errorf("解析失败必须返回零值，实际 %+v", got)
				}
				return
			}
			if got.Completed != tc.completed || got.Total != tc.total || got.SpeedBps != tc.speed || got.ETA != tc.eta {
				t.Errorf("解析结果 %+v，期望 completed=%d total=%d speed=%v eta=%d",
					got, tc.completed, tc.total, tc.speed, tc.eta)
			}
		})
	}
}

// TestParseAria2SizeAndETA 是上面那张表的补充：两个子解析器的边界（长后缀优先、纯数字、
// 单位缺失、非数字）单独钉一遍，免得它们在整行用例里被掩盖。
func TestParseAria2SizeAndETA(t *testing.T) {
	sizes := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0B", 0, true},
		{"512B", 512, true},
		{"2KiB", 2048, true},
		{"3MiB", 3 << 20, true},
		{"1GiB", 1 << 30, true},
		{"1.5MB", 1500000, true},
		{"2M", 2 << 20, true},
		{"384", 384, true}, // 纯数字按字节
		{"", 0, false},
		{"MiB", 0, false},
		{"-1MiB", 0, false},
		{"1XiB", 0, false},
	}
	for _, tc := range sizes {
		got, ok := parseAria2Size(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseAria2Size(%q) = (%v, %v)，期望 (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	etas := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"13s", 13, true},
		{"1m20s", 80, true},
		{"2h3m4s", 2*3600 + 3*60 + 4, true},
		{"1h", 3600, true},
		{"0s", 0, true},
		{"", 0, false},
		{"13", 0, false},   // 没有单位：不接受（aria2 不会这么写）
		{"1m2", 0, false},  // 末尾数字没结算
		{"1x", 0, false},   // 未知单位
		{"m20s", 0, false}, // 段内缺数字
	}
	for _, tc := range etas {
		got, ok := parseAria2ETA(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseAria2ETA(%q) = (%v, %v)，期望 (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// aria2TestCDN 起一个声明了固定长度的假 CDN：downloadWithAria2c 末尾会复核产物长度，
// 假 aria2c 必须写出恰好这么多字节，否则用例会绕在「长度不符」这条无关错误上。
func aria2TestCDN(t *testing.T, declared int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(declared))
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(make([]byte, declared))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeFakeAria2c 写一个假 aria2c：把 body 追加进脚本（shell 片段），并保证产物文件存在。
func writeFakeAria2c(t *testing.T, dir, body, dest string, size int) string {
	t.Helper()
	script := filepath.Join(dir, "aria2c")
	src := "#!/bin/sh\n" + body + fmt.Sprintf("dd if=/dev/zero of=%q bs=1 count=%d 2>/dev/null\n", dest, size)
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// TestAria2cSummaryLinesReachObserver 是**接线**用例，按**真形态**喂：摘要块写在 aria2c 的
// **stdout** 上（t34 修正：上一轮把它当 stderr，因此真实运行里一帧都收不到）。
//
// 夹具直接用抓到的原文块（头行 / === 分隔 / 进度行 / FILE: / --- 分隔 / 空行），再加：
//   - stderr 臂（对照）：一行摘要 + 一行错误 → 证明 stderr 也接进了泵、错误行照旧转发；
//   - stdout 上的非摘要行 → 证明 stdout 真的接进了泵（不是只接 stderr）。
//
// 断言：观察者收到帧（含 std 手臂那帧，Key=产物路径、字段与原文一致）；摘要块（含块尾空行）
// 不再出现在下游；两条错误/提示行原样转发；返回时事件已到齐（泵在 Close 里等干）。
//
// 变异验证：去掉 downloadWithAria2c 的 cmd.Stdout = pump（只剩 stderr 接泵）→ 本用例红
// （stdout 臂的帧全丢，这正是上一轮的缺陷）；去掉 cmd.Stderr = pump → stderr 臂的帧与
// 错误行断言红。
func TestAria2cSummaryLinesReachObserver(t *testing.T) {
	skipWithoutPOSIXShell(t)
	const size = 4
	const mib = 1 << 20
	cdn := aria2TestCDN(t, size)
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	body := strings.Join([]string{
		// ---- 真形态：摘要块在 stdout ----
		"echo '*** Download Progress Summary as of Sun Oct 05 12:00:00 2026 ***'",
		"echo '==============================================================================='",
		"echo '[#7d3f21 16KiB/19MiB(0%) CN:1 DL:16KiB ETA:20m19s]'",
		"echo 'FILE: out.bin'",
		"echo '-------------------------------------------------------------------------------'",
		"echo ''",
		"echo '[#7d3f21 512KiB/19MiB(2%) CN:1 DL:32KiB ETA:9m30s]'",
		"echo ''",
		// ---- 对照臂：stderr 上的摘要与非摘要行 ----
		"echo '[#7d3f21 1MiB/19MiB(5%) CN:1 DL:64KiB ETA:4m]' >&2",
		"echo 'aria2c: a forwarded error line' >&2",
		"echo 'aria2c: a forwarded stdout notice'",
	}, "\n") + "\n"
	script := writeFakeAria2c(t, dir, body, dest, size)

	// 下游用可捕获的 buffer：断言「摘要被消费、错误行被转发」。
	var forwarded bytes.Buffer
	orig := aria2ProgressDownstream
	aria2ProgressDownstream = func() io.Writer { return &forwarded }
	t.Cleanup(func() { aria2ProgressDownstream = orig })

	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)
	cfg := DownloadConfig{Client: newTestClient(), UseAria2c: true, Aria2cPath: script}
	if err := DownloadFile(ctx, cdn.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("假 aria2c 正常退出，不该报错: %v", err)
	}

	events := rec.snapshot()
	if len(events) != 3 {
		t.Fatalf("期望 3 帧（stdout 两帧 + stderr 一帧），实际 %d 帧：%+v", len(events), events)
	}
	for i, ev := range events {
		if ev.Key != dest {
			t.Errorf("第 %d 帧的 Key = %q，期望产物路径 %q（服务端按身份累计任务级字节）", i, ev.Key, dest)
		}
		if ev.Total != 19*mib {
			t.Errorf("第 %d 帧 Total = %d，期望 %d", i, ev.Total, 19*mib)
		}
	}
	// 首帧来自 stdout 的真形态块：字段逐项与原文一致（16KiB/19MiB、DL:16KiB、ETA:20m19s）。
	//（ETA 不在 ProgressEvent 里：那不是观察者契约的字段；解析器层面已由解析表钉住。）
	if got := events[0]; got.Current != 16*1024 || got.SpeedBps != 16*1024 {
		t.Errorf("stdout 摘要帧 = %+v，期望 Current=16KiB SpeedBps=16KiB", got)
	}
	if got := events[1]; got.Current != 512*1024 || got.SpeedBps != 32*1024 {
		t.Errorf("第二帧 = %+v，期望 Current=512KiB SpeedBps=32KiB", got)
	}
	// 末帧来自 stderr 臂：证明 stderr 也接在泵上（两条流缺一不可）。
	if got := events[2]; got.Current != 1*mib || got.SpeedBps != 64*1024 {
		t.Errorf("stderr 臂的帧 = %+v，期望 Current=1MiB SpeedBps=64KiB", got)
	}

	out := forwarded.String()
	for _, consumed := range []string{"Download Progress Summary", "16KiB/19MiB", "FILE: out.bin", "===========", "-----------"} {
		if strings.Contains(out, consumed) {
			t.Errorf("摘要块内容 %q 没有被消费（每秒一屏噪声）：%q", consumed, out)
		}
	}
	for _, want := range []string{"aria2c: a forwarded error line", "aria2c: a forwarded stdout notice"} {
		if !strings.Contains(out, want) {
			t.Errorf("非摘要行 %q 必须原样转发，实际转发内容 %q", want, out)
		}
	}
	// 摘要块尾部的空行也属于被消费的块：下游不该留下每秒一个空行。
	if strings.HasPrefix(out, "\n") || strings.Contains(out, "\n\n") {
		t.Errorf("下游出现空行：摘要块尾部空行没有被消费干净：%q", out)
	}
}

// TestAria2cGarbledSummaryDegradesSilently：摘要块形态变了（解析失败）时**静默降级**——
// 不报错、不发事件、不污染既有事件流；同路 stdout/stderr 上的其它行照旧转发。
//
// 变异验证：把解析失败的分支改成 return err（或在泵里 panic）→ 本用例红。
func TestAria2cGarbledSummaryDegradesSilently(t *testing.T) {
	skipWithoutPOSIXShell(t)
	const size = 4
	const mib = 1 << 20
	cdn := aria2TestCDN(t, size)
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	body := strings.Join([]string{
		// 真形态（stdout）：一行可解析 + 一行畸形方括号行 + 一行普通提示。
		"echo '*** Download Progress Summary as of Sun Oct 05 12:00:00 2026 ***'",
		"echo '[#7d3f21 16KiB/19MiB(0%) CN:1 DL:16KiB ETA:20m19s]'",
		"echo '[#7d3f21 not-a-progress-line]'",
		"echo 'aria2c: a stdout notice that must survive'",
		"echo 'aria2c: real error goes here' >&2",
	}, "\n") + "\n"
	script := writeFakeAria2c(t, dir, body, dest, size)

	var forwarded bytes.Buffer
	orig := aria2ProgressDownstream
	aria2ProgressDownstream = func() io.Writer { return &forwarded }
	t.Cleanup(func() { aria2ProgressDownstream = orig })

	rec := &observerRecorder{}
	ctx := WithProgressObserver(context.Background(), rec.observer)
	cfg := DownloadConfig{Client: newTestClient(), UseAria2c: true, Aria2cPath: script}
	if err := DownloadFile(ctx, cdn.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("畸形摘要行必须静默降级（不报错），实际: %v", err)
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("只有第一行是可解析的摘要行，期望恰好 1 帧，实际 %d 帧：%+v", len(events), events)
	}
	if got := events[0]; got.Current != 16*1024 || got.Total != 19*mib {
		t.Errorf("可解析那帧 = %+v，期望 Current=16KiB Total=19MiB", got)
	}
	for _, want := range []string{"aria2c: real error goes here", "aria2c: a stdout notice that must survive"} {
		if !strings.Contains(forwarded.String(), want) {
			t.Errorf("真实输出行 %q 被吞了：%q", want, forwarded.String())
		}
	}
}

// TestAria2cSummaryFlagOnlyWithObserver：--summary-interval 只在挂了观察者时加——
// 没有观察者时 aria2c 的命令行必须与改前逐字一致（摘要只会是终端噪声）。
//
// 变异验证：把 args 里的 if observer != nil 去掉（无条件加）→ 无观察者那一半红。
func TestAria2cSummaryFlagOnlyWithObserver(t *testing.T) {
	skipWithoutPOSIXShell(t)
	const size = 4
	cdn := aria2TestCDN(t, size)

	run := func(t *testing.T, withObserver bool) string {
		t.Helper()
		dir := t.TempDir()
		dest := filepath.Join(dir, "out.bin")
		argvFile := filepath.Join(dir, "argv.txt")
		body := "for a in \"$@\"; do echo \"$a\"; done > " + argvFile + "\n"
		script := writeFakeAria2c(t, dir, body, dest, size)

		ctx := context.Background()
		if withObserver {
			rec := &observerRecorder{}
			ctx = WithProgressObserver(ctx, rec.observer)
		}
		cfg := DownloadConfig{Client: newTestClient(), UseAria2c: true, Aria2cPath: script}
		if err := DownloadFile(ctx, cdn.URL+"/a.m4s", dest, cfg); err != nil {
			t.Fatalf("假 aria2c 正常退出，不该报错: %v", err)
		}
		data, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatalf("假 aria2c 没有写出 argv: %v", err)
		}
		return string(data)
	}

	without := run(t, false)
	if strings.Contains(without, "--summary-interval") {
		t.Errorf("没有观察者时不该加 --summary-interval（CLI 的 aria2c 路径必须与改前逐字一致）：\n%s", without)
	}
	with := run(t, true)
	if !strings.Contains(with, "--summary-interval=1") {
		t.Errorf("挂了观察者时必须加 --summary-interval=1（逐字节进度的唯一来源）：\n%s", with)
	}
	// 其它参数一个都不能动（任务 R 的约束⑤）：抽查几个关键参数仍在。
	for _, want := range []string{"--auto-file-renaming=false", "--download-result=hide", "--allow-overwrite=true", "--console-log-level=warn", "-x16", "--input-file=-"} {
		if !strings.Contains(with, want) {
			t.Errorf("既有 aria2c 参数 %q 不该消失：\n%s", want, with)
		}
	}
}
