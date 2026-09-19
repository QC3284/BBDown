package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件搬上游 DanmakuFilterTests 与 DanmakuAssEscapingTests 两张表。
// 弹幕内容来自其他用户、会被原样写进 ASS 的 Dialogue 行：{...} 是样式覆盖、\N 是换行，
// 不中和就能让一条弹幕改变别人的渲染，甚至破坏文件结构。

func TestUpstreamDanmakuFilter(t *testing.T) {
	makeItem := func(content, midHash string) DanmakuItem {
		return DanmakuItem{Content: content, MidHash: midHash}
	}
	items := DanmakuList{makeItem("广告：加群领福利", "10001"), makeItem("正常弹幕", "10001"), makeItem("XX广告", "10001")}
	got := FilterDanmaku(items, "广告", "")
	if len(got) != 1 || got[0].Content != "正常弹幕" {
		t.Errorf("关键词过滤 = %v，上游只留「正常弹幕」", got)
	}

	items = DanmakuList{makeItem("弹幕A", "111"), makeItem("弹幕B", "222"), makeItem("弹幕C", "111")}
	got = FilterDanmaku(items, "", "111")
	if len(got) != 1 || got[0].Content != "弹幕B" {
		t.Errorf("midHash 过滤 = %v，上游只留「弹幕B」", got)
	}

	items = DanmakuList{makeItem("a", "10001"), makeItem("b", "10001")}
	if got := FilterDanmaku(items, "", ""); len(got) != 2 {
		t.Errorf("无条件过滤应全留，实际 %d 条", len(got))
	}
	if got := FilterDanmaku(items, " ", " "); len(got) != 2 {
		t.Errorf("空白过滤条件应视同未设置（上游 IsNullOrWhiteSpace），实际 %d 条", len(got))
	}

	items = DanmakuList{makeItem("QQ群", "10001"), makeItem("正常", "10001"), makeItem("加微信", "10001")}
	got = FilterDanmaku(items, "QQ群,微信", "")
	if len(got) != 1 || got[0].Content != "正常" {
		t.Errorf("多关键词过滤 = %v", got)
	}

	items = DanmakuList{makeItem("广告", "111"), makeItem("正常", "111"), makeItem("正常", "222")}
	got = FilterDanmaku(items, "广告", "111")
	if len(got) != 1 || got[0].Content != "正常" || got[0].MidHash != "222" {
		t.Errorf("组合过滤 = %v，上游留「正常/222」", got)
	}
}

func renderASS(t *testing.T, items ...DanmakuItem) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.ass")
	if err := SaveDanmakuAsASS(items, path); err != nil {
		t.Fatalf("SaveDanmakuAsASS: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assDialogue(t *testing.T, ass string) string {
	t.Helper()
	for _, l := range strings.Split(ass, "\n") {
		if strings.HasPrefix(l, "Dialogue:") {
			return l
		}
	}
	t.Fatalf("未找到 Dialogue 行: %s", ass)
	return ""
}

// assText 取 Dialogue 行的正文（第 10 个逗号之后，上游同样的切法）。
func assText(t *testing.T, dialogue string) string {
	t.Helper()
	fields := strings.SplitN(dialogue, ",", 10)
	if len(fields) != 10 {
		t.Fatalf("Dialogue 字段数 = %d，上游要求 10: %s", len(fields), dialogue)
	}
	return fields[9]
}

func assItem(content string, second float64, color string) DanmakuItem {
	return DanmakuItem{
		Content:     content,
		Second:      second,
		StartTime:   computeDanmakuTime(second),
		EndTime:     computeDanmakuTime(second + moveSpendTime),
		DanmakuMode: posMove,
		Color:       color,
	}
}

func TestUpstreamAssStyleOverrideBracesNeutralized(t *testing.T) {
	ass := renderASS(t, assItem(`{\c&HFF0000&}被注入的红色`, 1.0, "FFFFFF"))
	text := assText(t, assDialogue(t, ass))
	if n := strings.Count(text, "{"); n != 1 {
		t.Errorf("正文里的 { 块数 = %d，上游要求 1: %s", n, text)
	}
	if strings.Contains(text, `{\c&HFF0000&}`) {
		t.Errorf("注入的样式块未被中和: %s", text)
	}
}

func TestUpstreamAssColorIsBgr(t *testing.T) {
	// B站弹幕色 255 = 0x0000FF（蓝）；ASS 是 &HBBGGRR，直接拼 #RRGGBB 会把蓝渲染成红。
	ass := renderASS(t, assItem("蓝色弹幕", 1.0, "0000FF"))
	dialogue := assDialogue(t, ass)
	if !strings.Contains(dialogue, `\c&HFF0000&`) {
		t.Errorf("蓝色应转为 \\c&HFF0000&: %s", dialogue)
	}
	if strings.Contains(dialogue, `\c&H0000FF&`) {
		t.Errorf("颜色字节序未反转: %s", dialogue)
	}

	ass = renderASS(t, assItem("白色弹幕", 1.0, "FFFFFF"))
	if d := assDialogue(t, ass); strings.Contains(d, `\c&`) {
		t.Errorf("白色不应生成颜色覆盖: %s", d)
	}
}

func TestUpstreamAssClosingBraceCannotBreakTagBlock(t *testing.T) {
	ass := renderASS(t, assItem(`}{\an5\fs100}放大`, 1.0, "FFFFFF"))
	text := assText(t, assDialogue(t, ass))
	if n := strings.Count(text, "{"); n != 1 {
		t.Errorf("{ 数 = %d，上游要求 1: %s", n, text)
	}
	if n := strings.Count(text, "}"); n != 1 {
		t.Errorf("} 数 = %d，上游要求 1: %s", n, text)
	}
}

func TestUpstreamAssNewlinesDoNotBreakStructure(t *testing.T) {
	ass := renderASS(t, assItem("第一行\n第二行\r\n第三行", 1.0, "FFFFFF"))
	lines := strings.Split(strings.ReplaceAll(ass, "\r\n", "\n"), "\n")
	eventsStart := -1
	for i, l := range lines {
		if l == "[Events]" {
			eventsStart = i
			break
		}
	}
	if eventsStart < 0 {
		t.Fatal("未找到 [Events] 段")
	}
	for _, l := range lines[eventsStart+1:] {
		if l == "" {
			continue
		}
		if !strings.HasPrefix(l, "Format:") && !strings.HasPrefix(l, "Dialogue:") {
			t.Errorf("正文换行泄漏成了独立行: %q", l)
		}
	}
}

func TestUpstreamAssNormalContentAndBackslash(t *testing.T) {
	ass := renderASS(t, assItem("内容", 1.0, "FFFFFF"))
	if d := assDialogue(t, ass); !strings.Contains(d, `\move(`) {
		t.Errorf("滚动弹幕应带 move 标签: %s", d)
	}
	if text := assText(t, assDialogue(t, ass)); !strings.Contains(text, "内容") {
		t.Errorf("正文未保留: %s", text)
	}

	ass = renderASS(t, assItem(`测试\N换行\h空格\b粗体`, 1.0, "FFFFFF"))
	text := assText(t, assDialogue(t, ass))
	if !strings.Contains(text, "测试＼N换行＼h空格＼b粗体") {
		t.Errorf("正文反斜杠未全角化: %s", text)
	}
}

func TestUpstreamAssTimestampsUseDotSeparator(t *testing.T) {
	// 上游在 de-DE 文化下断言：时间字段必须点号分隔且不含逗号（否则顶层逗号分隔会被
	// 小数逗号劈开，字段数不再是 10）。
	if got := computeDanmakuTime(12.34); got != "0:00:12.34" {
		t.Errorf("computeDanmakuTime(12.34) = %q，上游期望 0:00:12.34", got)
	}
	if got := computeDanmakuTime(12.34 + moveSpendTime); got != "0:00:20.34" {
		t.Errorf("结束时间 = %q，上游期望 0:00:20.34", got)
	}
	ass := renderASS(t, assItem("测试弹幕", 12.34, "FFFFFF"))
	dialogue := assDialogue(t, ass)
	fields := strings.SplitN(dialogue, ",", 10)
	if len(fields) != 10 {
		t.Fatalf("字段数 = %d，上游要求 10: %s", len(fields), dialogue)
	}
	if fields[1] != "0:00:12.34" || fields[2] != "0:00:20.34" {
		t.Errorf("时间字段 = %q/%q，上游期望 0:00:12.34/0:00:20.34", fields[1], fields[2])
	}
	if strings.Contains(fields[1], ",") || strings.Contains(fields[2], ",") {
		t.Errorf("时间字段含逗号: %q/%q", fields[1], fields[2])
	}
}
