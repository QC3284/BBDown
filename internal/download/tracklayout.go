package download

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// 流清单与标题行的排版：把「清晰度/分辨率/编码/帧率/码率/体积」排成固定列，段与段之间
// 用段标题（▎ 标题 + 计数）分隔。
//
// 为什么列宽要按**显示列**而不是 rune 数：清单里的标签是中英混排（"1080P 高清"、"8K 超高清"）。
// fmt 的 "%-12s" 按 rune 补空格，"1080P 高清"（8 rune / 10 列）与 "8K 超高清"（7 rune / 9 列）
// 都会补到 12 个 rune，但终端里占 14 与 13 列——列永远对不齐。
// 所以列宽一律走 DisplayWidth/PadDisplay（中文/全角 2 列、ASCII 1 列）。
//
// 为什么是固定列宽而不是按本次清单取最大值：formatVideoTrackLine/formatAudioTrackLine 是
// 单行纯函数，PrintSelectedTrack 也只拿到单条轨道；动态取宽要么改签名，要么让同一行在不同
// 调用点宽度不同。固定列宽的代价是超宽值（如 durl 路径的长 codecs 串）会让该行变宽——
// 数值列（帧率/码率/体积）用「右边缘固定」渲染，超宽时回收左侧填充，整行因此不散架；
// 文本列（清晰度/分辨率/编码）宁可这一行不齐，也不截断信息。
const (
	trackIndent      = " " // 内容行相对标题行缩进一格（标题行顶格，内容行缩进）
	trackPrefixWidth = 1   // 行首：序号 "0" / 选中标记 "*"
	trackNameWidth   = 12  // 清晰度（视频）/ 编码名（音频）；QualityMap 里最长是 "1080P 高帧率"（12 列）
	trackResWidth    = 9   // 分辨率（"1920x1080" 恰好 9 列）
	trackCodecsWidth = 4   // 视频编码：AVC/HEVC/AV1（"UNKNOWN" 兜底名超宽，按不截断策略处理）
	trackFPSWidth    = 5   // 帧率，右对齐（"15.01" 恰好 5 列）
	trackKbpsWidth   = 8   // 码率，右对齐
	trackSizeWidth   = 8   // 体积，右对齐
)

// trackColumnSep 是列间分隔（两个空格）。所有行共用同一个分隔，右对齐列才有稳定的右边缘。
const trackColumnSep = "  "

// Section 是一个段标题：1 字符竖条 + 段名 + 可选计数。
//
// 改前是「── 标题 ────────────」补到 45 列的横线：一条横线占掉半屏，段名看起来只是
// 横线的一部分，还要为它维护一个固定宽度。现在竖条 ▎ 就是「这一段从这里开始」——
// 它是**导航**（走 BRAND），段名是正文，计数是次要信息（MUTED），层级靠明度而不是横线长度。
//
// 宽终端下可选的那条 MUTED 短线**没有实现**：竖条 + 计数已经足够划出段落，再加横线只会
// 与表格右边缘抢视线（规范里的「不得喧宾夺主」）。这是有意的省略，不是漏掉。
const sectionBar = "▎"

// Section 的计数直接接在段名后面（不加空格）：全角括号自带视觉间隔，
// 多一个半角空格反而让标题看起来裂成两半。
type Section struct {
	Name  string
	Count string // 已经带括号的计数（如 "（6）"）；空串表示不显示
}

// Line 返回段标题的分段：竖条 BRAND + 段名 TEXT + 计数 MUTED。
func (s Section) Line() util.Line {
	line := util.NewLine().Add(util.TagBrand, sectionBar).Add(util.TagText, s.Name)
	return line.Add(util.TagMuted, s.Count)
}

// Plain 返回段标题的纯文本（无色能力、日志文件与纯文本用例断言用）。
func (s Section) Plain() string { return s.Line().Plain() }

// PrintSection 打印一个段标题（走内容通道：无时间戳、无缩进）。
func PrintSection(name, count string) {
	util.ContentLine(Section{Name: name, Count: count}.Line())
}

// FormatDurationShort 把秒数压成信息行里的短时长：不足一小时用 MM:SS（34:15），
// 超过一小时用 H:MM:SS（1:05:33）。util.FormatTime(absolute=true) 会给 "00:34:15"，
// 信息行里那个 "00:" 是纯噪音。
func FormatDurationShort(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// selectedMarker 是「已选择的流」那一行的行首标记。它占 1 列（与清单的序号列同宽），
// 走 BRAND 色，整行加粗——一眼能从清单里挑出真正要下的那一行。
const selectedMarker = "›"

// trackRow 是流清单里的一行。视频行六列全填；音频行没有分辨率/编码/帧率，留空占位——
// 正是这些占位让码率与体积两列在视频段与音频段之间也对齐。
//
// selected 只影响**样式**（前缀 BRAND、整行 BOLD），不影响任何一个字符：同一行的
// 无色形态与清单里的普通行逐字节同宽，列位置不因为「选中」而漂移。
type trackRow struct {
	prefix   string
	selected bool
	name     string
	res      string
	codecs   string
	fps      string
	kbps     string
	size     string
}

// trackLine 是一行表格的拼装器：记录当前显示列与行尾连续空格数，
// 供右对齐单元在超宽时回收左侧填充（见 right）。每一段都带自己的样式标签，
// 但**版式只由文本决定**——Render 与 Plain 逐字节对齐（去掉 SGR 后相等）。
type trackLine struct {
	parts    util.Line
	width    int
	trailing int // parts 末尾连续空格的个数（全部是 ASCII，字节数即显示列数）
}

// put 追加一段带样式的文本。空串照样把 trailing 归零（与改前逐字节一致：
// 右对齐单元在补白之后还会 put 一次值，那次 put 决定行尾状态）。
func (l *trackLine) put(tag util.Tag, s string) {
	if s == "" {
		l.trailing = 0
		return
	}
	l.parts = append(l.parts, util.Part{Tag: tag, Text: s})
	l.width += DisplayWidth(s)
	l.trailing = 0
	for i := len(s) - 1; i >= 0 && s[i] == ' '; i-- {
		l.trailing++
	}
}

// dropTrailing 从行尾回收 n 个字符（按契约只可能是补白空格）：先从最后一段里扣，
// 扣空了就整段丢掉——段是右对齐列的左侧填充，回收它后面的列才能保持右边缘。
func (l *trackLine) dropTrailing(n int) {
	for n > 0 && len(l.parts) > 0 {
		last := &l.parts[len(l.parts)-1]
		if len(last.Text) <= n {
			n -= len(last.Text)
			l.parts = l.parts[:len(l.parts)-1]
			continue
		}
		last.Text = last.Text[:len(last.Text)-n]
		n = 0
	}
}

// right 写一个右对齐单元：值的右边缘固定在列尾。值比列宽时回收左侧填充（至少留一个空格），
// 这样「码率 3000 kbps 比列宽长」这种常见情况不会把后面的体积列整列推走。
func (l *trackLine) right(tag util.Tag, s string, width int) {
	if overflow := DisplayWidth(s) - width; overflow > 0 {
		drop := overflow
		if drop > l.trailing-1 {
			drop = l.trailing - 1
		}
		if drop > 0 {
			l.dropTrailing(drop)
			l.width -= drop
			l.trailing -= drop
		}
		l.put(tag, s)
		return
	}
	l.put(tag, strings.Repeat(" ", width-DisplayWidth(s))+s)
}

// center 写一个居中的单元（表头的数值列用）：余数为 1 时向右偏一格。
// 左右都要补满整列宽——只补左边会让这一列塌陷，后面的列跟着左移（表头与数据就错开了）。
func (l *trackLine) center(tag util.Tag, s string, width int) {
	n := width - DisplayWidth(s)
	left := (n + 1) / 2
	if left < 0 {
		left = 0
	}
	if left > 0 {
		l.put(tag, strings.Repeat(" ", left))
	}
	l.put(tag, s)
	if right := n - left; right > 0 {
		l.put(tag, strings.Repeat(" ", right))
	}
}

// trimRight 去掉行尾的空格段：右对齐的最后一列不留尾巴，行尾也不该有空白。
// 比 strings.TrimRight 多做一件事——把被裁空的段一并丢掉，免得行尾残留一个空段。
func (l *trackLine) trimRight() util.Line {
	parts := l.parts
	for len(parts) > 0 {
		last := &parts[len(parts)-1]
		trimmed := strings.TrimRight(last.Text, " ")
		if trimmed == "" {
			parts = parts[:len(parts)-1]
			continue
		}
		last.Text = trimmed
		break
	}
	return parts
}

// line 按固定列宽拼出一行（带样式），行尾空格去掉。
func (r trackRow) line() util.Line {
	var l trackLine
	// 行首缩进与未选中行的前缀都是正文（不着色）：不为看不见的字符发色码。
	l.put(util.TagText, trackIndent)
	prefixTag := util.TagText
	bodyTag := util.TagText
	if r.selected {
		prefixTag, bodyTag = util.TagBrand, util.TagTextBold
	}
	l.put(prefixTag, PadDisplay(r.prefix, trackPrefixWidth))
	l.put(bodyTag, trackColumnSep)
	l.put(bodyTag, PadDisplay(r.name, trackNameWidth))
	l.put(bodyTag, trackColumnSep)
	l.put(bodyTag, PadDisplay(r.res, trackResWidth))
	l.put(bodyTag, trackColumnSep)
	l.put(bodyTag, PadDisplay(r.codecs, trackCodecsWidth))
	l.put(bodyTag, trackColumnSep)
	l.right(bodyTag, r.fps, trackFPSWidth)
	l.put(bodyTag, trackColumnSep)
	l.right(bodyTag, r.kbps, trackKbpsWidth)
	l.put(bodyTag, trackColumnSep)
	l.right(bodyTag, r.size, trackSizeWidth)
	return l.trimRight()
}

// render 返回这一行的纯文本（无色形态；列宽/空格与着色形态完全一致）。
func (r trackRow) render() string { return r.line().Plain() }

// videoTrackHeader / audioTrackHeader 是两段清单的表头。
//
// 列位置与数据行完全共用：文本列的标签左对齐（与列起点齐），数值列的标签**居中**——
// 数值本身右对齐，标签贴在列尾会看起来像被推到右边，居中落在数值上方最稳。
// 音频段没有的三列（分辨率/编码/帧率）留空，码率/体积两列因此仍与视频段同列。
//
// 表头是**次要信息**：打印时整行走 MUTED（见 PrintAllTracks），字符串本身保持纯文本，
// 免得每个调用点都要自己拆段。
var (
	videoTrackHeader = trackHeaderRow("清晰度", "分辨率", "编码", "帧率", "码率", "体积")
	audioTrackHeader = trackHeaderRow("编码", "", "", "", "码率", "体积")
)

// trackHeaderRow 渲染表头行。
func trackHeaderRow(name, res, codecs, fps, kbps, size string) string {
	var l trackLine
	l.put(util.TagMuted, trackIndent)
	l.put(util.TagMuted, PadDisplay("#", trackPrefixWidth))
	l.put(util.TagMuted, trackColumnSep)
	l.put(util.TagMuted, PadDisplay(name, trackNameWidth))
	l.put(util.TagMuted, trackColumnSep)
	l.put(util.TagMuted, PadDisplay(res, trackResWidth))
	l.put(util.TagMuted, trackColumnSep)
	l.put(util.TagMuted, PadDisplay(codecs, trackCodecsWidth))
	l.put(util.TagMuted, trackColumnSep)
	l.center(util.TagMuted, fps, trackFPSWidth)
	l.put(util.TagMuted, trackColumnSep)
	l.center(util.TagMuted, kbps, trackKbpsWidth)
	l.put(util.TagMuted, trackColumnSep)
	l.center(util.TagMuted, size, trackSizeWidth)
	return l.trimRight().Plain()
}

// trackDur 取用于估算体积的时长：分P时长优先，缺失（0）时退回轨道自带时长（与改前同一规则）。
func trackDur(pageDur, trackDur int) int {
	if pageDur == 0 {
		return trackDur
	}
	return pageDur
}

// formatTrackKbps 给码率加单位（"206 kbps"）；0 表示接口没给，留空不写 "0 kbps"。
func formatTrackKbps(bandwidth int64) string {
	if bandwidth <= 0 {
		return ""
	}
	return fmt.Sprintf("%d kbps", bandwidth)
}

// formatVideoTrackRow 用给定的行首前缀渲染视频行（纯文本形态）。
func formatVideoTrackRow(prefix string, v entity.Video, pageDur int) string {
	return formatVideoTrackRowLine(prefix, false, v, pageDur).Plain()
}

// formatVideoTrackRowLine 是带样式的那份：清单的序号行与 PrintSelectedTrack 的 "›" 行
// 共用它，两处的列位置因此严格一致（只有样式不同）。
func formatVideoTrackRowLine(prefix string, selected bool, v entity.Video, pageDur int) util.Line {
	dur := trackDur(pageDur, v.Dur)
	size := v.Size
	if size <= 0 {
		// 播放接口不给 size 时按 时长 × 码率 估算（码率是 kbps，这里沿用上游的 1024）。
		size = float64(dur) * float64(v.Bandwidth) * 1024 / 8
	}
	return trackRow{
		prefix:   prefix,
		selected: selected,
		name:     v.Dfn,
		res:      v.Res,
		codecs:   v.Codecs,
		fps:      formatTrackFPS(v.FPS),
		kbps:     formatTrackKbps(v.Bandwidth),
		size:     util.FormatFileSize(size),
	}.line()
}

// formatVideoTrackLine 渲染流清单里的一条视频流（行首是序号，纯文本）。
func formatVideoTrackLine(index int, v entity.Video, pageDur int) string {
	return formatVideoTrackRow(fmt.Sprintf("%d", index), v, pageDur)
}

// formatVideoTrackLineStyled 是清单行（未选中）的带样式形态：整行正文色，不含任何色码。
func formatVideoTrackLineStyled(index int, v entity.Video, pageDur int) util.Line {
	return formatVideoTrackRowLine(fmt.Sprintf("%d", index), false, v, pageDur)
}

// formatAudioTrackRow 用给定的行首前缀渲染音频行（纯文本）。音频没有分辨率/编码/帧率三列，
// 编码名（mp4a.40.2 等）落在「清晰度」列；其余列留空以便与视频行对齐。
func formatAudioTrackRow(prefix string, a entity.Audio, pageDur int) string {
	return formatAudioTrackRowLine(prefix, false, a, pageDur).Plain()
}

// formatAudioTrackRowLine 是带样式的那份（见 formatVideoTrackRowLine）。
func formatAudioTrackRowLine(prefix string, selected bool, a entity.Audio, pageDur int) util.Line {
	dur := trackDur(pageDur, a.Dur)
	return trackRow{
		prefix:   prefix,
		selected: selected,
		name:     a.Codecs,
		kbps:     formatTrackKbps(a.Bandwidth),
		size:     util.FormatFileSize(float64(dur) * float64(a.Bandwidth) * 1024 / 8),
	}.line()
}

// formatAudioTrackLine 渲染流清单里的一条音频流（行首是序号，纯文本）。
func formatAudioTrackLine(index int, a entity.Audio, pageDur int) string {
	return formatAudioTrackRow(fmt.Sprintf("%d", index), a, pageDur)
}

// formatAudioTrackLineStyled 是清单行（未选中）的带样式形态。
func formatAudioTrackLineStyled(index int, a entity.Audio, pageDur int) util.Line {
	return formatAudioTrackRowLine(fmt.Sprintf("%d", index), false, a, pageDur)
}

// formatTrackFPS 把接口给的帧率收敛到两位小数：playurl 有的端点给 "30"、有的给 "15.009"，
// 不统一的话帧率列会参差不齐。
//
// 用十进制四舍五入而不是 %.2f：14.925 的 float64 存储值略小于 14.925，%.2f 会截成 14.92，
// 而用户算的是「14.925 → 14.93」。非数值（"30000/1001"）原样保留，不硬凑。
func formatTrackFPS(fps string) string {
	t := strings.TrimSpace(fps)
	if t == "" {
		return ""
	}
	if _, err := strconv.ParseFloat(t, 64); err != nil {
		return t
	}
	if strings.ContainsAny(t, "eE") || !isPlainDecimal(t) {
		// 科学计数法/NaN/Inf 之类：退回 float 格式化，保证输出仍是数字。
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return t
		}
		return strconv.FormatFloat(f, 'f', 2, 64)
	}
	return roundDecimal(t, 2)
}

// isPlainDecimal 判断字符串是不是「[0-9]+(.[0-9]+)?」形态（允许前导负号）。
func isPlainDecimal(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	digits, dots := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digits++
		case s[i] == '.':
			dots++
			if dots > 1 || i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return digits > 0
}

// roundDecimal 对十进制数字符串做四舍五入，保留 digits 位小数（不足补零）。
// 全程在字符串上做，不经过 float——否则精度损失恰好发生在需要进位的那一位上。
func roundDecimal(s string, digits int) string {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	keep := frac
	if len(keep) > digits {
		keep = keep[:digits]
	}
	for len(keep) < digits {
		keep += "0"
	}
	all := intPart + keep
	if len(frac) > digits && frac[digits] >= '5' {
		all = incrementDigits(all)
	}
	var out string
	if digits == 0 {
		out = all
	} else {
		cut := len(all) - digits
		out = all[:cut] + "." + all[cut:]
	}
	if neg {
		out = "-" + out
	}
	return out
}

// incrementDigits 把十进制数字串加一（进位会自然增长长度："999" → "1000"）。
func incrementDigits(s string) string {
	b := []byte(s)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < '9' {
			b[i]++
			return string(b)
		}
		b[i] = '0'
	}
	return "1" + string(b)
}

// contentLinkWidth 是终端里直链提示行的显示宽度上限（80 列终端留出余量）。
const contentLinkWidth = 72

// ElideURL 把一条直链压到 width 显示列以内，供终端里显示（管道契约要求原始全文，见
// printStreamURL）：
//
//  1. 查询串（? 之后）是几百字符的主体，整段丢掉——但保留 "?" 与省略号，让用户看得出地址不完整；
//  2. 仍然超宽就把路径中段折叠成 …，保留主机与前段路径、以及文件名（两者才是识别一条流的信息）。
func ElideURL(raw string, width int) string {
	if width <= 0 || DisplayWidth(raw) <= width {
		return raw
	}
	base, queryDropped := raw, false
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base, queryDropped = base[:i], true
	}
	if queryDropped {
		if cand := base + "?…"; DisplayWidth(cand) <= width {
			return cand
		}
	}
	scheme, rest := "", base
	if i := strings.Index(base, "://"); i >= 0 {
		scheme, rest = base[:i+3], base[i+3:]
	}
	host, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		host, path = rest[:i], rest[i:]
	}
	name := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		name = path[i+1:]
	}
	prefix := scheme + host + "/…/"
	if room := width - DisplayWidth(prefix); room >= 1 {
		if DisplayWidth(name) <= room {
			// 文件名完整：中间那个 … 已经说明路径被折叠过，末尾不必再加一个。
			return prefix + name
		}
		return prefix + truncateDisplay(name, room-1) + "…"
	}
	return truncateDisplay(prefix, width) + "…"
}

// DisplayWidth 返回字符串在终端里占的列数：ASCII/半角 1 列，中文、全角与 emoji 2 列，
// 控制字符与组合记号 0 列。
//
// 导出是为了让编排层（internal/workflow 的任务卡）复用同一套宽度规则——workflow 本来就
// import download，不必为此新增跨层依赖，也不必在那边再养一份会走样的宽度表。
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeDisplayWidth(r)
	}
	return w
}

// runeDisplayWidth 是 DisplayWidth 的单字符版本。范围按 Unicode 的 East Asian Width
// 宽/全角块（W/F）与常见 emoji 块整理，够覆盖本项目的标签（清晰度名、[视频]/[音频]）。
func runeDisplayWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7F && r < 0xA0): // C0/C1 控制字符
		return 0
	case r == 0x200B || r == 0x200C || r == 0x200D || r == 0xFEFF: // 零宽字符
		return 0
	case r >= 0x0300 && r <= 0x036F: // 组合记号（如 e + U+0301）
		return 0
	}
	switch {
	case r >= 0x1100 && r <= 0x115F, // 谚文字母
		r >= 0x2E80 && r <= 0x303E,   // CJK 部首、康熙部首、CJK 符号与标点
		r >= 0x3041 && r <= 0x33FF,   // 假名、注音、CJK 兼容
		r >= 0x3400 && r <= 0x4DBF,   // CJK 扩展 A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK 基本区
		r >= 0xA000 && r <= 0xA4CF,   // 彝文
		r >= 0xAC00 && r <= 0xD7A3,   // 谚文音节
		r >= 0xF900 && r <= 0xFAFF,   // CJK 兼容表意文字
		r >= 0xFE10 && r <= 0xFE19,   // 竖排标点
		r >= 0xFE30 && r <= 0xFE6F,   // CJK 兼容形式
		r >= 0xFF00 && r <= 0xFF60,   // 全角 ASCII
		r >= 0xFFE0 && r <= 0xFFE6,   // 全角符号
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF, // emoji 补充
		r >= 0x20000 && r <= 0x3FFFD: // CJK 扩展 B 及以上
		return 2
	}
	return 1
}

// PadDisplay 在右侧补空格，让结果至少占 width 个显示列；已经够宽时原样返回——
// 宁可让这一行变宽，也不截断信息。
//
// 导出理由同 DisplayWidth：编排层的任务卡要按同一套显示宽度对齐，左对齐的列用它。
func PadDisplay(s string, width int) string {
	if n := width - DisplayWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// PadDisplayLeft 在左侧补空格（右对齐用）；超宽同样原样返回——宁可这一行变宽，
// 也不截断标签。导出理由同 DisplayWidth/PadDisplay：编排层的任务卡要把标签右对齐到
// 同一套显示宽度上（「输出路径」是 4 个全角字符 = 8 列），两边不能各算一套。
func PadDisplayLeft(s string, width int) string {
	if n := width - DisplayWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// truncateDisplay 按显示列截断（不补省略号）：保证 DisplayWidth(结果) <= width。
// 与 PadDisplay 同一套宽度规则，只是方向相反。
func truncateDisplay(s string, width int) string {
	w := 0
	for i, r := range s {
		rw := runeDisplayWidth(r)
		if w+rw > width {
			return s[:i]
		}
		w += rw
	}
	return s
}
