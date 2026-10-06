package workflow

import (
	"regexp"
	"strings"
	"testing"

	"github.com/QC3284/BBDown-Go/internal/entity"
	"github.com/QC3284/BBDown-Go/internal/util"
)

// ansiRe 去掉颜色码：宽度与缩进断言都要在**可见文本**上做。
var ansiRe = regexp.MustCompile("\\x1b\\[[0-9;]*[a-zA-Z]")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// TestPrintVideoHeaderWrapsLongCJKTitle 逐字钉住 t64 ①（标题行折行）：
//   - 80 列下长 CJK 标题折行，断点按显示宽度（CJK 2 列）；
//   - 首行是「[时间戳] - 视频标题: <片段>」，续行缩进恰好 28 列（与时间戳对齐）；
//   - 每行（含时间戳）不超过终端宽度，且片段拼回去等于原标题（**不丢字**）；
//   - 短标题仍然单行，输出与改前一致。
//
// 变异验证：撤掉折行（回到单行 util.LogColor）→ 本用例红（出现 >80 列的行、且没有续行）。
func TestPrintVideoHeaderWrapsLongCJKTitle(t *testing.T) {
	restore := util.SetTerminalWidthForTest(80)
	defer restore()

	title := "《明日方舟：终末地》核心章节「丹青渡」版本PV — 4K HDR 试听与全曲目对照"
	const prefix = "视频标题: "

	out := stripANSI(captureStdout(t, func() {
		printVideoHeader(&entity.VInfo{Title: title}, false)
	}))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("80 列下长标题应当折成多行，实际 %d 行：%q", len(lines), out)
	}
	if !strings.Contains(lines[0], prefix) {
		t.Errorf("首行应当带「%s」：%q", prefix, lines[0])
	}
	for i, line := range lines {
		if w := util.DisplayWidth(line); w > 80 {
			t.Errorf("第 %d 行 %d 列 > 80：%q", i, w, line)
		}
	}
	// 续行缩进恰好 28 列（前 28 列是空格，紧接着就是正文）。
	pad := strings.Repeat(" ", util.LogIndentWidth)
	for i, line := range lines[1:] {
		if !strings.HasPrefix(line, pad) {
			t.Errorf("续行 %d 没有 28 列缩进：%q", i+1, line)
			continue
		}
		if strings.HasPrefix(line[len(pad):], " ") {
			t.Errorf("续行 %d 的缩进多于 28 列：%q", i+1, line)
		}
	}
	// 不丢字。
	idx := strings.Index(lines[0], prefix)
	joined := lines[0][idx+len(prefix):]
	for _, line := range lines[1:] {
		joined += strings.TrimPrefix(line, pad)
	}
	if joined != title {
		t.Errorf("折行丢了字：拼回 %q，want %q", joined, title)
	}

	// 短标题：仍然单行（与改前逐字一致）。
	short := stripANSI(captureStdout(t, func() {
		printVideoHeader(&entity.VInfo{Title: "示例稿件"}, false)
	}))
	shortLines := strings.Split(strings.TrimRight(short, "\n"), "\n")
	if len(shortLines) != 1 || !strings.HasSuffix(shortLines[0], prefix+"示例稿件") {
		t.Errorf("短标题不该折行：%q", short)
	}
}

// TestPrintVideoHeaderWrapRespectsNarrowTerminal 窄终端（40 列）也要守约：
// 折块预算 = 终端宽 - 28 缩进 - 前缀宽，行宽不超过终端宽（不因为窄就压成空行）。
func TestPrintVideoHeaderWrapRespectsNarrowTerminal(t *testing.T) {
	restore := util.SetTerminalWidthForTest(40)
	defer restore()
	title := strings.Repeat("标题测试", 10)

	out := stripANSI(captureStdout(t, func() {
		printVideoHeader(&entity.VInfo{Title: title}, false)
	}))
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := util.DisplayWidth(line); w > 40 {
			t.Errorf("40 列终端下第 %d 行 %d 列 > 40：%q", i, w, line)
		}
		if strings.TrimSpace(line) == "" {
			t.Errorf("第 %d 行是空行（不该压成空行）", i)
		}
	}
}
