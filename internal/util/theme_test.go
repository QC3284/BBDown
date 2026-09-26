package util

import (
	"regexp"
	"strings"
	"testing"
)

// 本文件是**颜色角色的 golden 守卫**：设计规范里的 5 个角色（BRAND / MUTED / TEXT /
// 成功 / 警告 / 错误）、256↔16 的降级、以及「NO_COLOR / 非 TTY / TERM=dumb 一律无色」。
//
// 断言写的是**字面字节**（"\x1b[38;5;45m"），不是引用实现里的常量：引用常量等于把
// 「实现改了但两端一起改」也判绿，那是假绿。角色色号是本仓对用户可见的契约，改动必须
// 同时改用例与规范本身。
//
// 注入能力判定是必须的：CI 的 stdout 恒为管道，真实判定恒为无色，不注入的话
// 「着色」这一半永远测不到（等于只有降级那一半被测）。

// sgrRe 匹配一条 SGR 序列（含颜色与加粗）。
var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// colorsOn256 把「终端 + 256 色」钉死，用例结束自动还原。
func colorsOn256(t *testing.T) {
	t.Helper()
	t.Cleanup(ForceColorsForTest())
}

// colorsOn16 把「终端 + 只支持 16 色」钉死（TERM 里没有 256、也没有 COLORTERM）。
func colorsOn16(t *testing.T) {
	t.Helper()
	restoreTerm := SetTerminalForTest(func() bool { return true })
	restoreEnv := SetColorEnvForTest(func() ColorEnv { return ColorEnv{Term: "xterm"} })
	t.Cleanup(func() { restoreEnv(); restoreTerm() })
}

// TestRolePaletteIsGolden 钉住 256 色档的角色色号（规范原文）：
// BRAND 45 / MUTED 245 / 成功 78 / 警告 214 / 错误 203；TEXT 不发任何序列。
//
// 变异验证：把任一角色的色号改掉（如 MUTED 245 → 250）→ 对应用例变红。
func TestRolePaletteIsGolden(t *testing.T) {
	colorsOn256(t)

	cases := []struct {
		name string
		tag  Tag
		want string
	}{
		{"BRAND", TagBrand, "\x1b[38;5;45m"},
		{"BRAND+BOLD", TagBrandBold, "\x1b[1;38;5;45m"},
		{"MUTED", TagMuted, "\x1b[38;5;245m"},
		{"成功", TagSuccess, "\x1b[38;5;78m"},
		{"警告", TagWarn, "\x1b[38;5;214m"},
		{"错误", TagError, "\x1b[38;5;203m"},
	}
	for _, c := range cases {
		want := c.want + "x" + AnsiReset
		if got := c.tag.Apply("x"); got != want {
			t.Errorf("%s: Apply = %q，期望 %q", c.name, got, want)
		}
	}

	// TEXT 是终端默认色：不额外设色，也不该「发一条复位充数」。
	if got := TagText.Apply("x"); got != "x" {
		t.Errorf("TEXT 不该带任何序列：%q", got)
	}
	// 加粗是独立的强调手段：TEXT+BOLD 只发 SGR 1。
	if got := TagTextBold.Apply("x"); got != "\x1b[1mx"+AnsiReset {
		t.Errorf("TEXT+BOLD 应只发 SGR 1：%q", got)
	}
}

// TestRolePaletteFallsBackTo16Colors 钉住降级链的第二档：256 色不可用时落到 16 色亮色档
// （BRAND→96、MUTED→90、成功→92、警告→93、错误→91），而不是把 "38;5;45" 原样打给
// 只懂 16 色的终端（那会显示成一串字面量）。
//
// 变异验证：colorCode 去掉 color16 分支（或返回 256 码）→ 本用例红。
func TestRolePaletteFallsBackTo16Colors(t *testing.T) {
	colorsOn16(t)

	cases := []struct {
		name string
		tag  Tag
		want string
	}{
		{"BRAND", TagBrand, "\x1b[96m"},
		{"BRAND+BOLD", TagBrandBold, "\x1b[1;96m"},
		{"MUTED", TagMuted, "\x1b[90m"},
		{"成功", TagSuccess, "\x1b[92m"},
		{"警告", TagWarn, "\x1b[93m"},
		{"错误", TagError, "\x1b[91m"},
	}
	for _, c := range cases {
		want := c.want + "x" + AnsiReset
		if got := c.tag.Apply("x"); got != want {
			t.Errorf("%s（16 色档）: Apply = %q，期望 %q", c.name, got, want)
		}
	}
	if strings.Contains(TagBrand.Apply("x"), "38;5;") {
		t.Errorf("16 色档不该出现 256 色序列：%q", TagBrand.Apply("x"))
	}
}

// TestColorDepthDecidesByEnvironment 钉住「能不能着色」的判定本身（注入快照，不读真实环境）：
// 非 TTY、NO_COLOR（有且非空）、TERM=dumb 三种情况一律 ColorNone；NO_COLOR 为空串不算禁用
// （no-color.org 的约定是「有且非空」）。
func TestColorDepthDecidesByEnvironment(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		env  ColorEnv
		want ColorDepth
	}{
		{"非 TTY（管道/重定向）", false, ColorEnv{Term: "xterm-256color"}, ColorNone},
		{"NO_COLOR=1", true, ColorEnv{NoColor: true, Term: "xterm-256color"}, ColorNone},
		{"TERM=dumb", true, ColorEnv{Term: "dumb"}, ColorNone},
		{"TERM 大小写不敏感", true, ColorEnv{Term: "DUMB"}, ColorNone},
		{"TERM=xterm-256color", true, ColorEnv{Term: "xterm-256color"}, Color256},
		{"COLORTERM=truecolor", true, ColorEnv{Term: "xterm", ColorTerm: "truecolor"}, Color256},
		{"TERM=xterm（只支持 16 色）", true, ColorEnv{Term: "xterm"}, Color16},
		{"TERM 为空（保守回退 16 色）", true, ColorEnv{}, Color16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreTerm := SetTerminalForTest(func() bool { return c.tty })
			restoreEnv := SetColorEnvForTest(func() ColorEnv { return c.env })
			defer func() { restoreEnv(); restoreTerm() }()

			if got := CurrentColorDepth(); got != c.want {
				t.Errorf("CurrentColorDepth = %v，期望 %v", got, c.want)
			}
			if got, want := ColorsEnabled(), c.want != ColorNone; got != want {
				t.Errorf("ColorsEnabled = %v，期望 %v", got, want)
			}
		})
	}
}

// TestThemeEmitsNoBackgroundCodes 钉住「不使用背景色」：主题能发出的 SGR 序列只有
// 加粗、各角色的前景色、以及复位——没有任何 4x/10x 背景码。
//
// 变异验证：给任一角色加背景（如 BRAND 用 "\x1b[44m"）→ 白名单断言红。
func TestThemeEmitsNoBackgroundCodes(t *testing.T) {
	colorsOn256(t)
	allowed := map[string]bool{
		"\x1b[0m": true, "\x1b[1m": true,
		"\x1b[38;5;45m": true, "\x1b[1;38;5;45m": true,
		"\x1b[38;5;245m": true, "\x1b[1;38;5;245m": true,
		"\x1b[38;5;78m": true, "\x1b[1;38;5;78m": true,
		"\x1b[38;5;203m": true, "\x1b[1;38;5;203m": true,
		"\x1b[38;5;214m": true, "\x1b[1;38;5;214m": true,
	}
	line := NewLine().
		Add(TagBrand, "a").Add(TagBrandBold, "b").
		Add(TagMuted, "c").Add(TagMutedBold, "d").
		Add(TagSuccess, "e").Add(TagSuccessBold, "f").
		Add(TagWarn, "g").Add(TagWarnBold, "h").
		Add(TagError, "i").Add(TagErrorBold, "j").
		Add(TagText, "k").Add(TagTextBold, "l")
	rendered := line.Render()
	seqs := sgrRe.FindAllString(rendered, -1)
	if len(seqs) == 0 {
		t.Fatalf("能力允许时应当着色：%q", rendered)
	}
	for _, seq := range seqs {
		if !allowed[seq] {
			t.Errorf("主题打出了白名单之外的 SGR 序列 %q（背景色是禁止的）：%q", seq, rendered)
		}
	}
}

// TestLinePlainAndRenderDifferOnlyBySGR 是降级链的地基不变量：同一行的两个视图
// **只差 SGR 序列**——去掉转义后必须逐字节相等，版式（列宽/空格/字符）一字不动。
//
// 变异验证：让 Render 在无色时返回别的东西（如把段拼接顺序换掉）→ 本用例红。
func TestLinePlainAndRenderDifferOnlyBySGR(t *testing.T) {
	line := NewLine().
		Add(TagBrand, "▎").Add(TagText, "可用流").Add(TagMuted, "（6）").
		Add(TagTextBold, "  34:15").Add(TagError, "✗ 失败")

	colorsOn256(t)
	styled := line.Render()
	if got, want := sgrRe.ReplaceAllString(styled, ""), line.Plain(); got != want {
		t.Errorf("着色视图去掉 SGR 后应等于纯文本视图：\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(styled, "\x1b[") {
		t.Errorf("能力允许时应当着色：%q", styled)
	}
	if line.Plain() != "▎可用流（6）  34:15✗ 失败" {
		t.Errorf("纯文本视图不符：%q", line.Plain())
	}
}

// TestNoColorsUnderDegradationChain 是硬性要求里那条用例：**NO_COLOR / 非 TTY / TERM=dumb
// 下输出里不得出现任何 ANSI**，且版式（去掉转义后的文本）与非降级时逐字节相同。
//
// 三个条件分别驱动一遍事件行与内容行；内容行走真实通道（ContentLine）而不是只测 Render，
// 因为「降级链去掉」的典型改法就是把判定从通道里挪回渲染里。
//
// 变异验证：colorText/render 里去掉 ColorsEnabled 判定（无条件着色）→ 三条子用例全红。
func TestNoColorsUnderDegradationChain(t *testing.T) {
	pinLogClock(t)
	l := NewLogger(func() bool { return true })
	line := NewLine().
		Add(TagBrand, "▎").Add(TagText, "可用流").Add(TagMuted, "（6）").
		Add(TagTextBold, "  34:15").Add(TagError, "✗ 失败")

	cases := []struct {
		name string
		tty  bool
		env  ColorEnv
	}{
		{"NO_COLOR=1", true, ColorEnv{NoColor: true, Term: "xterm-256color"}},
		{"非 TTY", false, ColorEnv{Term: "xterm-256color"}},
		{"TERM=dumb", true, ColorEnv{Term: "dumb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreTerm := SetTerminalForTest(func() bool { return c.tty })
			restoreEnv := SetColorEnvForTest(func() ColorEnv { return c.env })
			defer func() { restoreEnv(); restoreTerm() }()

			out := captureStdout(t, func() {
				l.LogWarn("警告")
				l.LogError("失败")
				ContentLine(line)
			})
			if strings.Contains(out, "\x1b") {
				t.Errorf("%s 下不该有任何 ANSI 序列：%q", c.name, out)
			}
			// 版式与非降级时一致（无色只是不着色，不是少打东西）。
			for _, want := range []string{"⚠ 警告", "✗ 失败", "▎可用流（6）  34:15✗ 失败"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s 下缺少 %q：%q", c.name, want, out)
				}
			}
		})
	}
}

// TestSetColorEnvForTestRestores：注入的环境快照用完必须还原（否则同包后续用例会读到
// 被钉死的值：本机绿、CI 红这类漂移）。
func TestSetColorEnvForTestRestores(t *testing.T) {
	before := readColorEnv()
	restore := SetColorEnvForTest(func() ColorEnv { return ColorEnv{NoColor: true} })
	if !readColorEnv().NoColor {
		t.Error("注入后应当读到被钉死的快照")
	}
	restore()
	if readColorEnv() != before {
		t.Errorf("还原后应回到注入前的快照（%+v），实际 %+v", before, readColorEnv())
	}
}
