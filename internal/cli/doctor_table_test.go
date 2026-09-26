package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/util"
)

// doctor 的表格化排版：状态符号（1 列）+ 名称列（按本次最长名称对齐）+ 详情列；详情超宽折行，
// 续行缩进到详情列。--json 分支是机读契约，一字不动（另见 doctor_json_test.go）。

var doctorLogPrefixRe = regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}\] - `)

var doctorAnsiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// doctorUndecorated 去掉日志时间戳与 ANSI 色码，只留消息本体（控制台断言用）。
func doctorUndecorated(line string) string {
	return doctorAnsiRe.ReplaceAllString(doctorLogPrefixRe.ReplaceAllString(line, ""), "")
}

// stubDoctorChecksWith 把自检项换成给定的固定结果（离线、可预期），用例结束自动还原。
func stubDoctorChecksWith(t *testing.T, results ...doctorResult) {
	t.Helper()
	orig := doctorChecks
	doctorChecks = make([]func(context.Context, config.MyOption, *util.HTTPClient) doctorResult, len(results))
	for i, res := range results {
		doctorChecks[i] = func(context.Context, config.MyOption, *util.HTTPClient) doctorResult { return res }
	}
	t.Cleanup(func() { doctorChecks = orig })
}

// doctorColumnAt 返回 needle 在 line 里的起始显示列，并校验它与前几行一致。
func doctorColumnAt(t *testing.T, line, needle, what string, want, row int) int {
	t.Helper()
	at := strings.Index(line, needle)
	if at < 0 {
		t.Fatalf("第 %d 行缺少%s内容 %q：%q", row, what, needle, line)
	}
	got := download.DisplayWidth(line[:at])
	if want >= 0 && got != want {
		t.Errorf("%s起点应一致：第 %d 行 %d 列，期望 %d 列：%q", what, row, got, want, line)
	}
	return got
}

// TestDoctorTableColumnsAlign：三个等级、三种名称宽度，名称列与详情列的起始列必须一致。
//
// 变异验证：doctorMark 改回 [ok]/[warn]/[fail]（三档宽度不同）→ 符号断言变红；把 renderDoctorRows
// 的名称补位去掉（不按最长名称对齐）→ 详情列一致性断言变红；把 download.PadDisplay 改成直接返回
// 原串（宽度表单一来源被破坏，cli 与 workflow 同时受影响）→ 详情列一致性断言同样变红。
func TestDoctorTableColumnsAlign(t *testing.T) {
	results := []doctorResult{
		{"ffmpeg/mp4box", "ok", "/usr/bin/ffmpeg；杜比视界混流: 是"},
		{"输出目录", "warn", "/tmp/work 可写，但剩余空间仅 0.8 GiB"},
		{"接口/登录态", "fail", "请求 api.bilibili.com 失败：connection refused"},
	}
	wantMark := []string{"+", "!", "x"}

	rows := renderDoctorRows(results)
	if len(rows) != len(results) {
		t.Fatalf("每个自检项一行，实际 %d 行", len(rows))
	}
	nameCol, detailCol := -1, -1
	for i, res := range results {
		row := rows[i]
		if row.Level != res.Level {
			t.Errorf("第 %d 行的等级应原样带出：%q != %q", i+1, row.Level, res.Level)
		}
		if len(row.Lines) != 1 {
			t.Fatalf("短详情不应折行：%q", row.Lines)
		}
		line := row.Lines[0]
		if !strings.HasPrefix(line, wantMark[i]+" ") {
			t.Errorf("第 %d 行应以符号 %q 开头：%q", i+1, wantMark[i], line)
		}
		nameCol = doctorColumnAt(t, line, res.Name, "名称列", nameCol, i+1)
		detailCol = doctorColumnAt(t, line, res.Detail, "详情列", detailCol, i+1)
	}
	if nameCol != doctorMarkWidth+1 {
		t.Errorf("名称列应从 %d 列开始（符号 %d 列 + 1 空格），实际 %d 列", doctorMarkWidth+1, doctorMarkWidth, nameCol)
	}
	// 最长名称 "ffmpeg/mp4box" 占 13 列 → 详情列 = 符号 + 空格 + 名称列 + 2 列间隔。
	if want := doctorMarkWidth + 1 + 13 + 2; detailCol != want {
		t.Errorf("详情列应从 %d 列开始，实际 %d 列", want, detailCol)
	}
}

// TestDoctorTableWrapsLongDetail：详情超宽要折行，续行缩进到详情列；折行只断行、不丢字符。
//
// 变异验证：去掉 renderDoctorRows 里的 wrapDisplay 调用（详情一律一行）→ 折行与行宽断言变红。
func TestDoctorTableWrapsLongDetail(t *testing.T) {
	long := strings.Repeat("未找到可用的混流工具；", 20) // 无空格的中文，便于逐字复原
	results := []doctorResult{
		{"桩", "ok", "短"},
		{"较长的名称", "fail", long},
	}
	rows := renderDoctorRows(results)
	row := rows[1]
	if len(row.Lines) < 2 {
		t.Fatalf("超宽详情必须折行，实际 %d 行：%q", len(row.Lines), row.Lines)
	}
	detailCol := doctorMarkWidth + 1 + download.DisplayWidth("较长的名称") + 2

	var tail strings.Builder
	for i, line := range row.Lines {
		if w := download.DisplayWidth(line); w > doctorWrapWidth {
			t.Errorf("第 %d 行 %d 列，超过 doctorWrapWidth=%d：%q", i+1, w, doctorWrapWidth, line)
		}
		if i == 0 {
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		if indent := download.DisplayWidth(line[:len(line)-len(trimmed)]); indent != detailCol {
			t.Errorf("续行应缩进到详情列（%d 列），实际 %d 列：%q", detailCol, indent, line)
		}
		tail.WriteString(trimmed)
	}
	// 首行的详情部分 + 各续行 = 原详情：折行不放、不丢（对照「详情不折行」的旧形态）。
	head := "x " + "较长的名称" + "  " // 该名称恰是最长的一个，无需补位
	if !strings.HasPrefix(row.Lines[0], head) {
		t.Fatalf("首行应以 %q 开头：%q", head, row.Lines[0])
	}
	if got := row.Lines[0][len(head):] + tail.String(); got != long {
		t.Errorf("折行后内容应逐字复原：得到 %q", got)
	}

	t.Run("收窄 doctorWrapWidth 后折得更碎", func(t *testing.T) {
		orig := doctorWrapWidth
		doctorWrapWidth = 40
		t.Cleanup(func() { doctorWrapWidth = orig })
		narrow := renderDoctorRows(results)[1].Lines
		for i, line := range narrow {
			if w := download.DisplayWidth(line); w > 40 {
				t.Errorf("第 %d 行 %d 列，超过收窄后的 40 列：%q", i+1, w, line)
			}
		}
		if len(narrow) <= len(row.Lines) {
			t.Errorf("收窄后应折出更多行：%d → %d", len(row.Lines), len(narrow))
		}
	})
}

// TestDoctorSymbolsMatchLevels 钉住三档状态符号，并确认旧括号标记彻底消失。
//
// 变异验证：doctorMark 改回 [ok]/[warn]/[fail] → 本用例红。
func TestDoctorSymbolsMatchLevels(t *testing.T) {
	for _, tc := range []struct{ level, mark string }{{"ok", "+"}, {"warn", "!"}, {"fail", "x"}} {
		if got := doctorMark(tc.level); got != tc.mark {
			t.Errorf("等级 %s 的符号应为 %q，实际 %q", tc.level, tc.mark, got)
		}
		line := renderDoctorRows([]doctorResult{{"项", tc.level, "详情"}})[0].Lines[0]
		if !strings.HasPrefix(line, tc.mark+" ") {
			t.Errorf("等级 %s 的行应以 %q 开头：%q", tc.level, tc.mark+" ", line)
		}
		for _, old := range []string{"[ok]", "[warn]", "[fail]"} {
			if strings.Contains(line, old) {
				t.Errorf("旧括号标记 %q 不该再出现：%q", old, line)
			}
		}
	}
}

// TestDoctorConsoleRowsAreAligned：走一遍真实输出路径（runDoctor → util 日志 → 控制台），
// 去掉时间戳与色码后名称列、详情列仍对齐、符号仍正确——防止表格只活在纯函数里。
func TestDoctorConsoleRowsAreAligned(t *testing.T) {
	stubs := []doctorResult{
		{"ffmpeg/mp4box", "ok", "桩：工具就绪"},
		{"输出目录", "warn", "桩：余量偏低"},
		{"接口/登录态", "fail", "桩：被风控拦截(HTTP 412)"},
	}
	stubDoctorChecksWith(t, stubs...)

	out := captureStdout(t, func() { runDoctor(context.Background(), config.DefaultMyOption(), nil) })

	var lines []string
	for _, raw := range strings.Split(out, "\n") {
		line := doctorUndecorated(raw)
		if len(line) > 1 && strings.Contains("+!x", line[:1]) && line[1] == ' ' {
			lines = append(lines, line)
		}
	}
	if len(lines) != len(stubs) {
		t.Fatalf("控制台应有 %d 行表格行，实际 %d 行：%q", len(stubs), len(lines), lines)
	}
	wantMark := []string{"+", "!", "x"}
	nameCol, detailCol := -1, -1
	for i, res := range stubs {
		line := lines[i]
		if !strings.HasPrefix(line, wantMark[i]+" ") {
			t.Errorf("第 %d 行应以符号 %q 开头：%q", i+1, wantMark[i], line)
		}
		nameCol = doctorColumnAt(t, line, res.Name, "名称列", nameCol, i+1)
		detailCol = doctorColumnAt(t, line, res.Detail, "详情列", detailCol, i+1)
	}
	if nameCol != doctorMarkWidth+1 || detailCol != doctorMarkWidth+1+13+2 {
		t.Errorf("控制台上的列起点不对：名称列 %d、详情列 %d", nameCol, detailCol)
	}
}

// TestDoctorJSONBranchKeepsItsContract：--json 的机器可读契约一字不动——表格化只改人类可读
// 输出；JSON 仍是纯 JSON（无符号、无对齐空格、无「提示/命令」标签），字段顺序与缩进照旧。
//
// 变异验证：让 runDoctorJSON 也走 renderDoctorRows（把表格塞进机读输出）→ 逐字节比对变红。
// 将来若真的给 doctorResult 加字段，这份期望要在同一次改动里一起更新（= 有意的契约变更）。
func TestDoctorJSONBranchKeepsItsContract(t *testing.T) {
	stubDoctorChecksWith(t,
		doctorResult{"假工具", "ok", "桩：一切正常"},
		doctorResult{"假接口", "fail", "桩：被风控拦截(HTTP 412)"},
	)
	var buf bytes.Buffer
	code := runDoctorJSON(context.Background(), config.DefaultMyOption(), nil, &buf)
	if code != 1 {
		t.Fatalf("有 fail 项时退出码应为 1，实际 %d", code)
	}
	want := `[
  {
    "name": "假工具",
    "level": "ok",
    "detail": "桩：一切正常"
  },
  {
    "name": "假接口",
    "level": "fail",
    "detail": "桩：被风控拦截(HTTP 412)"
  }
]
`
	if got := buf.String(); got != want {
		t.Errorf("--json 输出应与表格化前逐字节一致：\n得到 %q\n期望 %q", got, want)
	}
}
