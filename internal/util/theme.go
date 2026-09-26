package util

import (
	"fmt"
	"os"
	"strings"
)

// 本文件是 CLI 的视觉主题：颜色角色、样式标签、以及「能力判定 + 降级链」。
//
// 设计规范（本仓 CLI 视觉的基线，也是用例钉住的对象）：
//
//   - **只有 5 个角色**：BRAND（导航与进度）、MUTED（次要信息）、TEXT（正文，默认色），
//     外加只表示状态的 成功 / 警告 / 错误。**不使用任何背景色**。
//   - **层级靠明度，不靠颜色数量**：MUTED 次要（标签/单位/时间戳/表头/统计）→
//     TEXT 内容 → BOLD 关键值；BRAND 只出现在导航与进度上；状态色只用于状态。
//   - **降级链**：非 TTY / NO_COLOR（有且非空）/ TERM=dumb → 完全无色，且版式与列宽
//     一字不变（着色的只是同一段文本，Plain 与 Render 只差 SGR 序列）；
//     256 色不可用（TERM 里没有 256、也没有 COLORTERM）→ 16 色回退。
//   - **机器契约面逐字节不变**：-I 在管道里的裸直链、--print-urls、--info-json、
//     --progress-json 都走裸 fmt.Print 或内容通道；内容通道在无色能力下输出的就是
//     这些接口看到的纯文本。
//
// 能力判定做成可注入（SetTerminalForTest / SetColorEnvForTest）：CI 的 stdout 恒为管道，
// 真机判定恒为假，不注入的话「着色」与「降级」两条路径都测不到（等于没有用例）。

// Role 是颜色角色。TEXT 是终端默认色（不额外发 SGR），其余角色只在能力允许时发色。
type Role int

const (
	RoleText Role = iota
	RoleBrand
	RoleMuted
	RoleSuccess
	RoleWarn
	RoleError
)

// Tag 是一段文本的样式：颜色角色 + 可选的加粗（SGR 1）。零值 Tag{} 就是「正文、不加粗」。
//
// 用它而不是直接拼 SGR 序列：序列只在这里出现一次，拼错一个数字就是全终端可见的差异，
// 而调用点写 util.TagMuted 时不可能拼错。
type Tag struct {
	Role Role
	Bold bool
}

// 预置样式。调用点只用这一组，不自己构造 Tag（Bold 组合只在这里穷举一次）。
var (
	TagText        = Tag{Role: RoleText}
	TagTextBold    = Tag{Role: RoleText, Bold: true}
	TagBrand       = Tag{Role: RoleBrand}
	TagBrandBold   = Tag{Role: RoleBrand, Bold: true}
	TagMuted       = Tag{Role: RoleMuted}
	TagMutedBold   = Tag{Role: RoleMuted, Bold: true}
	TagSuccess     = Tag{Role: RoleSuccess}
	TagSuccessBold = Tag{Role: RoleSuccess, Bold: true}
	TagWarn        = Tag{Role: RoleWarn}
	TagWarnBold    = Tag{Role: RoleWarn, Bold: true}
	TagError       = Tag{Role: RoleError}
	TagErrorBold   = Tag{Role: RoleError, Bold: true}
)

// 角色色码（冒号形态的 SGR 参数，不含 "\033[" 与 "m"）与 16 色回退档。
//
// 256 色：BRAND 45 = 亮青、MUTED 245 = 中灰、成功 78 = 绿、警告 214 = 橙、错误 203 = 红。
// 回退档取 8 位亮色 96/90/92/93/91 —— 任何只支持 16 色的终端（TERM=xterm、screen、
// 老式串口终端）都能显示，且与 256 档是同一个色相家族，降级后层级不变。
const (
	codeBrand256   = "38;5;45"
	codeBrand16    = "96"
	codeMuted256   = "38;5;245"
	codeMuted16    = "90"
	codeSuccess256 = "38;5;78"
	codeSuccess16  = "92"
	codeWarn256    = "38;5;214"
	codeWarn16     = "93"
	codeError256   = "38;5;203"
	codeError16    = "91"
)

// ColorEnv 是颜色能力判定的环境输入快照（单独一个类型是为了让用例注入，不读真实环境）。
type ColorEnv struct {
	NoColor   bool   // NO_COLOR：按 no-color.org 的约定只看「有且非空」，不取取值
	Term      string // TERM；"dumb" 表示哑终端
	ColorTerm string // COLORTERM；非空表示终端自称支持真彩/256 色
}

// defaultColorEnv 读真实环境。
func defaultColorEnv() ColorEnv {
	return ColorEnv{
		NoColor:   os.Getenv("NO_COLOR") != "",
		Term:      os.Getenv("TERM"),
		ColorTerm: os.Getenv("COLORTERM"),
	}
}

// readColorEnv 做成变量：用例注入固定环境后，「降级链」的每一档都能被断言。
var readColorEnv = defaultColorEnv

// SetColorEnvForTest 注入颜色能力判定的环境输入（fn 为 nil 表示还原），返回还原函数，
// 调用方必须 defer 它。不注入的话判定读的是跑测试这台机器的真实环境，
// 「NO_COLOR=1 必须无色」这条用例在 CI 上就是碰运气。
func SetColorEnvForTest(fn func() ColorEnv) (restore func()) {
	prev := readColorEnv
	if fn == nil {
		readColorEnv = defaultColorEnv
	} else {
		readColorEnv = fn
	}
	return func() { readColorEnv = prev }
}

// ForceColorsForTest 把「终端 + 256 色」一次性钉死（跨包用例断言着色形态时的默认手法），
// 返回还原函数，调用方必须 defer 它。
func ForceColorsForTest() (restore func()) {
	restoreTerm := SetTerminalForTest(func() bool { return true })
	restoreEnv := SetColorEnvForTest(func() ColorEnv {
		return ColorEnv{Term: "xterm-256color", ColorTerm: "truecolor"}
	})
	return func() { restoreEnv(); restoreTerm() }
}

// ColorDepth 是进程当前的颜色档位。
type ColorDepth int

const (
	ColorNone ColorDepth = iota // 完全无色（非 TTY / NO_COLOR / TERM=dumb）
	Color16                     // 16 色回退
	Color256                    // 256 色
)

// CurrentColorDepth 按降级链算当前档位：先判「能不能着色」，再判 256 还是 16。
//
// TERM 为空（某些 Windows 终端、CI）时不算 256：宁可回退到 16 色的亮色档，
// 也不要在只懂 16 色的终端上打出一串 "38;5;45" 让用户看到字面量。
func CurrentColorDepth() ColorDepth {
	env := readColorEnv()
	if !IsTerminalOut() || env.NoColor || strings.EqualFold(env.Term, "dumb") {
		return ColorNone
	}
	if env.ColorTerm != "" || strings.Contains(env.Term, "256") {
		return Color256
	}
	return Color16
}

// ColorsEnabled 报告当前是否输出颜色（降级链的唯一开关）。
func ColorsEnabled() bool { return CurrentColorDepth() != ColorNone }

// colorCode 返回角色在当前档位下的 SGR 参数；无色档或 TEXT 角色返回空串。
func (t Tag) colorCode() string {
	var c256, c16 string
	switch t.Role {
	case RoleBrand:
		c256, c16 = codeBrand256, codeBrand16
	case RoleMuted:
		c256, c16 = codeMuted256, codeMuted16
	case RoleSuccess:
		c256, c16 = codeSuccess256, codeSuccess16
	case RoleWarn:
		c256, c16 = codeWarn256, codeWarn16
	case RoleError:
		c256, c16 = codeError256, codeError16
	default:
		return ""
	}
	switch CurrentColorDepth() {
	case Color256:
		return c256
	case Color16:
		return c16
	}
	return ""
}

// code 返回这一样式的完整 SGR 序列（加粗与颜色合成一条 CSI），
// 「正文且不加粗」或无色档下返回空串——**没有序列**才是无色形态，而不是发一条复位。
//
// 能力判定必须在最前面：加粗（SGR 1）同样是转义序列，NO_COLOR / 管道 / TERM=dumb 下
// 一个字节都不该发出去（「只发加粗不发颜色」也是污染下游解析的 ANSI）。
func (t Tag) code() string {
	if CurrentColorDepth() == ColorNone {
		return ""
	}
	var params []string
	if t.Bold {
		params = append(params, "1")
	}
	if c := t.colorCode(); c != "" {
		params = append(params, c)
	}
	if len(params) == 0 {
		return ""
	}
	return "\033[" + strings.Join(params, ";") + "m"
}

// Apply 把样式包在文本外层（含复位）；无色档、空文本或纯正文样式下原样返回。
//
// 复位紧跟正文、排在任何换行之前：套在整段末尾时它会落到下一行行首，把紧随其后的
// 裸直链（fmt.Println 打的）污染成 "\033[0mhttp://…"，脚本按 ^http 取行全部落空。
func (t Tag) Apply(text string) string {
	code := t.code()
	if code == "" || text == "" {
		return text
	}
	return code + text + AnsiReset
}

// Part 是一行里的一段文本及其样式。
type Part struct {
	Tag  Tag
	Text string
}

// Line 是一行由若干段拼成的文本。
//
// **不变量**：Plain() 与 Render() 只差 SGR 序列——同一个 Line 的两种视图逐字符对齐
// （去掉转义序列后 Render() 必须与 Plain() 逐字节相等）。降级链与机器契约面都靠它：
// 无色能力下内容通道输出的就是 Plain()，与着色版一字不差。
type Line []Part

// NewLine 建一个空行（Line{} 的语义化写法）。
func NewLine() Line { return Line{} }

// Add 追加一段；空文本直接跳过（不为看不见的段发色码）。
func (l Line) Add(tag Tag, text string) Line {
	if text == "" {
		return l
	}
	return append(l, Part{Tag: tag, Text: text})
}

// Addf 是 Add 的格式化版。
func (l Line) Addf(tag Tag, format string, args ...interface{}) Line {
	return l.Add(tag, fmt.Sprintf(format, args...))
}

// AddLine 把另一行接在后面（进度帧 = 进度条 + 信息段这类组合）。
func (l Line) AddLine(other Line) Line { return append(l, other...) }

// compact 合并相邻的同标签段：表格一行的每个单元各是一段，不合并会在选中行上打出
// 十几条重复的 "\033[1m…\033[0m"（视觉相同，字节数翻倍，还让 golden 断言难读）。
func (l Line) compact() Line {
	if len(l) < 2 {
		return l
	}
	out := make(Line, 0, len(l))
	for _, p := range l {
		if n := len(out); n > 0 && out[n-1].Tag == p.Tag {
			out[n-1].Text += p.Text
			continue
		}
		out = append(out, p)
	}
	return out
}

// Plain 返回无色文本（版式真相：列宽、空格、字符都在这里）。
func (l Line) Plain() string {
	var b strings.Builder
	for _, p := range l.compact() {
		b.WriteString(p.Text)
	}
	return b.String()
}

// Render 返回当前能力下的文本；无色档下与 Plain() 逐字节相同。
func (l Line) Render() string {
	c := l.compact()
	if !ColorsEnabled() {
		var b strings.Builder
		for _, p := range c {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	var b strings.Builder
	for _, p := range c {
		b.WriteString(p.Tag.Apply(p.Text))
	}
	return b.String()
}

// Sanitize 按内容通道的约定清洗每一段（服务端可控文本里的换行/ANSI 不得伪造出一行）。
// 干净文本原样通过——表格的补白空格、卡片的对齐空格都不会被折叠。
func (l Line) Sanitize() Line {
	out := make(Line, len(l))
	for i, p := range l {
		out[i] = Part{Tag: p.Tag, Text: SanitizeLogString(p.Text)}
	}
	return out
}

// Block 是多行样式文本（任务卡这类整块排版）：每行一个 Line。
type Block []Line

// Add 追加一行。
func (b Block) Add(l Line) Block { return append(b, l) }

// Plain 返回无色文本，每行一个换行（末尾也有）。
func (b Block) Plain() string {
	var sb strings.Builder
	for _, l := range b {
		sb.WriteString(l.Plain())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Render 返回当前能力下的文本；无色档下与 Plain() 逐字节相同。
func (b Block) Render() string {
	var sb strings.Builder
	for _, l := range b {
		sb.WriteString(l.Render())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Sanitize 逐段清洗（见 Line.Sanitize）。
func (b Block) Sanitize() Block {
	out := make(Block, len(b))
	for i, l := range b {
		out[i] = l.Sanitize()
	}
	return out
}
