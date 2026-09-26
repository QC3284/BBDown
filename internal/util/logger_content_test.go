package util

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 本文件钉住「事件通道 vs 内容通道」的分工（见 logger.go 的两段说明）：
//   - 事件（日志）带时间戳，默认只到时分秒，日期与毫秒只在 --debug 下出现；
//   - 内容（标题/表格/卡片/汇总）不带任何前缀，着色只在 TTY 生效。
//
// 时间戳靠可注入时钟断言（SetLogClock）：墙钟跑两次可能跨秒，毫秒更是必然不同。

// fixedLogTime 是用例钉死的「现在」（含毫秒，用来证明默认档位不输出它）。
func fixedLogTime() time.Time {
	return time.Date(2026, 9, 26, 19, 19, 41, 967000000, time.Local)
}

// pinLogClock 把事件时钟钉死，用例结束自动还原。
func pinLogClock(t *testing.T) {
	t.Helper()
	restore := SetLogClock(fixedLogTime)
	t.Cleanup(restore)
}

// TestEventTimestampShowsTimeOnlyByDefault：默认档位只有时分秒——日期与毫秒对看日志的人
// 没有价值，还把每一行的内容推后 16 列。
//
// 变异验证：eventTimeLayout 改回 "2006-01-02 15:04:05.000" → 前缀断言与「不含日期/毫秒」断言红。
func TestEventTimestampShowsTimeOnlyByDefault(t *testing.T) {
	pinLogClock(t)
	l := NewLogger(nil)
	out := captureStdout(t, func() { l.Log("事件 %d", 1) })

	if want := "[19:19:41] 事件 1\n"; out != want {
		t.Errorf("默认事件行不符：\n得到 %q\n期望 %q", out, want)
	}
	for _, absent := range []string{"2026-09-26", ".967", " - "} {
		if strings.Contains(out, absent) {
			t.Errorf("默认档位不该出现 %q：%q", absent, out)
		}
	}
}

// TestEventTimestampShowsDateAndMillisInDebug：--debug 下才给全量（日期 + 毫秒），
// 排查时序问题时才有意义。
//
// 变异验证：debug 分支改成复用默认布局 → 前缀断言红。
func TestEventTimestampShowsDateAndMillisInDebug(t *testing.T) {
	pinLogClock(t)
	l := NewLogger(func() bool { return true })
	out := captureStdout(t, func() { l.Log("事件") })

	if want := "[2026-09-26 19:19:41.967] 事件\n"; out != want {
		t.Errorf("--debug 事件行不符：\n得到 %q\n期望 %q", out, want)
	}
}

// TestLogFileAlwaysKeepsFullTimestamp：日志**文件**与终端两档不同——文件是事后对账用的，
// 无论 --debug 与否都写全量时间戳（跨天的记录只留 HH:MM:SS 就没法定位）。
//
// 变异验证：appendToFile 改用默认（时分秒）前缀 → 本用例红。
func TestLogFileAlwaysKeepsFullTimestamp(t *testing.T) {
	pinLogClock(t)
	path := filepath.Join(t.TempDir(), "run.log")
	l := NewLogger(nil)
	l.SetLogFile(path)

	captureStdout(t, func() { l.Log("落盘事件") })

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读日志文件：%v", err)
	}
	if want := "[2026-09-26 19:19:41.967] 落盘事件\n"; string(body) != want {
		t.Errorf("文件日志行不符：\n得到 %q\n期望 %q", string(body), want)
	}
}

// TestContentLinesCarryNoTimestamp：内容行不带时间戳、不带缩进，管道里也不带颜色。
//
// 变异验证：Content 改走 Log（套日志前缀）→ 逐字节断言红。
func TestContentLinesCarryNoTimestamp(t *testing.T) {
	pinLogClock(t)

	if out := captureStdout(t, func() { Content("内容 %s", "x") }); out != "内容 x\n" {
		t.Errorf("内容行应当是裸文本 + 换行：%q", out)
	}
	// 非终端：着色参数被忽略（管道里不该出现 ANSI）。
	out := captureStdout(t, func() { ContentColored(ContentCyan, "青色内容") })
	if out != "青色内容\n" {
		t.Errorf("非终端的内容行应当是纯文本：%q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("非终端不该输出 ANSI 色码：%q", out)
	}
}

// TestContentColorOnlyOnTerminalAndResetsBeforeNewline：TTY 下才着色，且颜色复位必须排在
// 换行**之前**——套在整段末尾时复位会落到下一行行首，紧随其后的裸直链（fmt.Println 打的）
// 就变成 "\x1b[0mhttp://…"，脚本按 ^http 取行全部落空。
//
// 变异验证：contentText 把 AnsiReset 拼在 text 之后（不拆换行）→ 最后两条断言红。
func TestContentColorOnlyOnTerminalAndResetsBeforeNewline(t *testing.T) {
	restore := SetTerminalForTest(func() bool { return true })
	t.Cleanup(restore)

	out := captureStdout(t, func() { ContentColored(ContentCyan, "青色内容") })
	if want := AnsiCyan + "青色内容" + AnsiReset + "\n"; out != want {
		t.Errorf("TTY 下的内容行不符：\n得到 %q\n期望 %q", out, want)
	}
	if !strings.HasSuffix(out, AnsiReset+"\n") {
		t.Errorf("颜色复位必须在换行之前：%q", out)
	}
	if strings.HasPrefix(out, AnsiReset) {
		t.Errorf("行首不该出现颜色复位：%q", out)
	}
}

// TestContentFinishesProgressLineFirst：内容通道与日志共用 ConsoleLock + 「先给进度行收尾」
// 的约定（consoleWrite）。少了它，卡片/表格会与原地重绘的进度条挤在同一行。
//
// 变异验证：Content 直接写 os.Stdout（绕过 consoleWrite）→ 本用例红。
func TestContentFinishesProgressLineFirst(t *testing.T) {
	SetProgressLineActive(true)
	t.Cleanup(func() { SetProgressLineActive(false) })

	out := captureStdout(t, func() { Content("内容") })
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("进度行还停在当前行时，内容行应先换行收尾：%q", out)
	}
}

// TestContentFollowsLogRedirect：机读模式（--info-json / doctor --json）把日志让位到 stderr，
// 内容通道必须跟着同一落点——否则表格会混进 stdout 的 JSON 数据流里。
//
// 变异验证：contentText 写死 os.Stdout → 本用例红。
func TestContentFollowsLogRedirect(t *testing.T) {
	var buf bytes.Buffer
	restore := RedirectConsoleLogs(&buf)
	Content("让位内容")
	restore()

	if want := "让位内容\n"; buf.String() != want {
		t.Errorf("让位期间内容应写进指定流：\n得到 %q\n期望 %q", buf.String(), want)
	}
}

// TestContentSanitizesServerText：标题/UP 名是服务端可控文本，里面的换行与 ANSI 序列
// 不能伪造出一行内容（上游 RF-54/RF-70 的同类加固）。
//
// 变异验证：contentText 去掉 sanitizeLogArgs → 换行计数断言红。
func TestContentSanitizesServerText(t *testing.T) {
	out := captureStdout(t, func() { Content("标题: %s", "第一行\n第二行\x1b[91m") })
	if got := strings.Count(out, "\n"); got != 1 {
		t.Errorf("内容行应当只有末尾一个换行（注入的换行要被清洗）：%q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("服务端文本里的 ANSI 序列必须被清洗：%q", out)
	}
}

// TestContentBlockKeepsMultilineLayout：任务卡这类整块排版原样落屏（多行的空白对齐
// 不能被单行清洗压平），且同样遵守进度行收尾约定。
func TestContentBlockKeepsMultilineLayout(t *testing.T) {
	SetProgressLineActive(true)
	t.Cleanup(func() { SetProgressLineActive(false) })

	block := "── 任务卡 ────\n 标题  x\n 音频流  M4A\n"
	out := captureStdout(t, func() { ContentBlock(block) })
	if want := "\n" + block; out != want {
		t.Errorf("多行内容块应原样写出：\n得到 %q\n期望 %q", out, want)
	}
}

// TestSetTerminalForTestRestores：注入的终端判定用完必须还原（否则同包后续用例会看到
// 被钉死的值，CI 与开发机表现不一致）。
func TestSetTerminalForTestRestores(t *testing.T) {
	before := IsTerminalOut()
	restore := SetTerminalForTest(func() bool { return true })
	if !IsTerminalOut() {
		t.Error("注入后应当读到 true")
	}
	restore()
	if IsTerminalOut() != before {
		t.Errorf("还原后应回到注入前的判定（%v），实际 %v", before, IsTerminalOut())
	}
}
