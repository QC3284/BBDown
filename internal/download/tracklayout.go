package download

import (
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// 流清单的排版：把「清晰度/分辨率/编码/帧率/码率/体积」排成固定列，并按终端宽度自适应。
//
// 为什么列宽要按**显示列**而不是 rune 数：清单里的标签是中英混排（"1080P 高清"、"8K 超高清"、
// "[视频]"）。fmt 的 "%-12s" 按 rune 补空格，"1080P 高清"（8 rune / 10 列）与 "8K 超高清"
// （7 rune / 9 列）都会补到 12 个 rune，但终端里占 14 与 13 列——列永远对不齐。
// 所以列宽一律走 DisplayWidth/PadDisplay（中文/全角 2 列、ASCII 1 列）。
//
// 为什么列宽是固定的而不是按本次清单取最大值：formatVideoTrackLine/formatAudioTrackLine 是
// 单行纯函数，PrintSelectedTrack 也只拿到单条轨道；动态取宽要么改签名，要么让同一行在不同
// 调用点宽度不同。代价是接口偶尔给出比列宽长的值（如分数帧率 "30000/1001"），这类列允许
// 溢出到 maxWidth（见 trackColumn.maxWidth），宽度预算按 maxWidth 算，整行因此仍不超终端宽度。
//
// 宽度从哪来：util.TerminalWidth()（每次渲染取一次，见 internal/util/terminal.go）。
// 放不下时的降级顺序（单一来源：newTrackPlan 对**所有**行——表头、数据行、已选择的流——
// 只算一次列集合）：
//
//  1. 先省列（体积 → 帧率 → 编码 → 分辨率），保住「清晰度 + 码率」这条选流主判据；
//  2. 还不够就压缩列宽（先把行首的纯填空从 7 收到 6）；
//  3. 最后才折行，续行悬挂缩进对齐到第一列数据（见 layoutCells）。

const (
	trackPrefixWidth = 7  // 行首：序号 "0."，或选中轨道的标签 "[视频]"/"[音频]"（6 列 + 1 空格）
	trackNameWidth   = 12 // 清晰度（视频）/ 编码名（音频）；QualityMap 里最长是 "1080P 高帧率"（12 列）
	trackResWidth    = 10 // 分辨率
	trackCodecsWidth = 7  // 视频编码：AVC/HEVC/AV1/UNKNOWN（兜底名恰好 7 列）
	// trackFPSWidth 是帧率列宽。6 列不是拍的：playurl 实际返回的是 "30.000"/"25.000"
	// （实测 2026-09 热门榜三条视频的 18 条视频流全是 6 列），少数是 "15.009"。
	// 列宽 3 时这些值每行都溢出 3 列，帧率之后的编码/码率/体积整列右移——表头一加就露馅。
	trackFPSWidth = 6
	// trackFPSMaxWidth 是帧率列的溢出上限：少数内容给 "30000/1001" 这类分数帧率（10 列），
	// 截断它会把帧率变成猜谜；宽度预算按 maxWidth 算，溢出因此仍不超终端宽度。
	trackFPSMaxWidth = 10
	trackKbpsWidth   = 10 // 码率，右对齐
	trackSizeWidth   = 11 // 体积，右对齐
)

// trackColumnSep 是列间分隔（两个空格）。所有行共用同一个分隔，右对齐列才有稳定的右边缘。
const trackColumnSep = "  "

// trackColumnSepWidth 是列间距的显示宽度（= DisplayWidth(trackColumnSep)）：
// 宽度预算里每列都要算上它，写成常量免得每行再算一次。
const trackColumnSepWidth = 2

// 列标识：行与表头按它取单元格。
const (
	colPrefix = "prefix"
	colName   = "name"
	colRes    = "res"
	colCodecs = "codecs"
	colFPS    = "fps"
	colKbps   = "kbps"
	colSize   = "size"
)

// trackColumn 是清单里的一列。
type trackColumn struct {
	key   string // col* 之一
	width int    // 布局宽度：左对齐列补空格到它，右对齐列在其中右对齐
	// maxWidth 是渲染时允许的最大宽度（>= width）：接口值略长于列宽时原样显示（不进 …），
	// 但它参与宽度预算，整行因此不会因为个别长值被推出终端。
	maxWidth int
	// minWidth 是压缩下限（极端窄终端才会用到；见 shrinkColumnsToFit）。
	minWidth int
	right    bool // 右对齐（数值列）
	// drop 是丢弃优先级：1 最先丢、4 最后丢、0 永不丢。
	// 依据是「这一列没了还能不能选流」：体积只是参考值；帧率/编码影响播放兼容性；
	// 分辨率与清晰度重复度高；清晰度与码率是选流的主判据，永不丢。
	drop int
}

// videoTrackColumns 是清单的列集合（音频段共用同一套几何：分辨率/编码/帧率三列留空占位，
// 码率与体积因此仍落在视频段的同一列上）。
var videoTrackColumns = []trackColumn{
	{key: colPrefix, width: trackPrefixWidth, maxWidth: trackPrefixWidth, minWidth: 6},
	{key: colName, width: trackNameWidth, maxWidth: trackNameWidth, minWidth: 6},
	{key: colRes, width: trackResWidth, maxWidth: trackResWidth, minWidth: 6, drop: 4},
	{key: colCodecs, width: trackCodecsWidth, maxWidth: trackCodecsWidth, minWidth: 4, drop: 3},
	{key: colFPS, width: trackFPSWidth, maxWidth: trackFPSMaxWidth, minWidth: trackFPSWidth, right: true, drop: 2},
	{key: colKbps, width: trackKbpsWidth, maxWidth: trackKbpsWidth, minWidth: 7, right: true},
	{key: colSize, width: trackSizeWidth, maxWidth: trackSizeWidth, minWidth: 8, right: true, drop: 1},
}

// videoTrackHeaders / audioTrackHeaders 是表头文字（键与列一一对应）。
//
// 音频段没有分辨率/编码/帧率（数据行留空占位），表头也只写它有的三列——但码率与体积
// 仍落在与视频段相同的列上，两段上下对照着看。
var (
	videoTrackHeaders = map[string]string{
		colName: "清晰度", colRes: "分辨率", colCodecs: "编码",
		colFPS: "帧率", colKbps: "码率", colSize: "体积",
	}
	audioTrackHeaders = map[string]string{
		colName: "编码", colKbps: "码率", colSize: "体积",
	}
)

// trackPlan 是一次渲染的列排版：终端宽度 + 行首缩进 + 保留的列。
//
// 整份清单（表头、所有数据行，以及「已选择的流」）共用同一个 plan：表头与数据因此必然对齐，
// 也不会出现「调用点不同、算出来的列不一样」。plan 只由宽度决定，与内容无关——同一次运行里
// 视频段、音频段、已选择的流看到的列集合完全一致。
type trackPlan struct {
	width   int // 终端宽度（列）
	indent  int // 行首缩进
	columns []trackColumn
}

// newTrackPlan 由终端宽度推出列排版。
func newTrackPlan(width int) trackPlan {
	if width <= 0 {
		width = util.DefaultTerminalWidth
	}
	indent := trackIndentFor(width)
	budget := width - indent
	cols := append([]trackColumn(nil), videoTrackColumns...)
	for trackRequiredWidth(cols) > budget {
		idx := nextDroppableColumn(cols)
		if idx < 0 {
			break
		}
		cols = append(cols[:idx], cols[idx+1:]...)
	}
	shrinkColumnsToFit(cols, budget)
	return trackPlan{width: width, indent: indent, columns: cols}
}

// minTrackContent 是缩进之后至少要留给数据的显示列数：正好够
// 「清晰度 + 分辨率 + 编码 + 码率」四列（体积与帧率是优先级最高、最先被省掉的两列）。
// 见 tracklayout_width_test.go 里钉住这个配对关系的用例。
const minTrackContent = 54

// trackIndentFor 计算清单行的行首缩进。
//
// 宽终端与日志前缀对齐（LogIndentWidth = 28 列，"[日期 时间.毫秒] - " 的长度）；但缩进只是
// 为对齐服务的装饰，不能把用户真正要看的列挤出屏幕：40 列终端上 28 列缩进只剩 12 列内容，
// 整行会退化成「只剩一个省略号」。所以缩进后留不下 minTrackContent 列时按需回收，
// 最多收到 0——数据列的可读性优先于缩进对齐。
//
// 单调：终端变宽时缩进不减少、内容区不缩小。否则会出现「终端更宽、反而少显示一列」的怪现象
// （缩进 28 少 28 列，回收缩进又多出 28 列）。
func trackIndentFor(width int) int {
	if width <= minTrackContent {
		return 0
	}
	if width-util.LogIndentWidth >= minTrackContent {
		return util.LogIndentWidth
	}
	return width - minTrackContent
}

// trackRequiredWidth 是这些列在最坏情况下占的显示宽度：用 maxWidth 而不是 width，
// 因为帧率这类列允许溢出到 maxWidth。预算按它算，任何一行的实际宽度都不会超过终端宽度。
func trackRequiredWidth(cols []trackColumn) int {
	total := 0
	for i, c := range cols {
		if i > 0 {
			total += trackColumnSepWidth
		}
		total += c.maxWidth
	}
	return total
}

// nextDroppableColumn 挑下一个该丢的列：drop 值最小（最先丢）且 > 0 的列；并列时取靠前的。
func nextDroppableColumn(cols []trackColumn) int {
	best, bestDrop := -1, 0
	for i, c := range cols {
		if c.drop <= 0 {
			continue
		}
		if best < 0 || c.drop < bestDrop {
			best, bestDrop = i, c.drop
		}
	}
	return best
}

// shrinkColumnsToFit 在所有可丢列都丢光之后仍放不下时压缩列宽。
//
// 顺序是「纯填空优先」：行首列 7 → 6 列只是少一个空格，不影响任何信息；之后才轮到
// 清晰度、分辨率这些真正承载信息的列（截断时补 …）。宽终端上永远走不到这里。
func shrinkColumnsToFit(cols []trackColumn, budget int) {
	for i := range cols {
		for trackRequiredWidth(cols) > budget && cols[i].maxWidth > cols[i].minWidth {
			cols[i].maxWidth--
			if cols[i].width > cols[i].maxWidth {
				cols[i].width = cols[i].maxWidth
			}
		}
	}
}

// hangingIndent 是折行后续行的缩进：对齐到第一列数据（清晰度/编码列）的起点，
// 不是顶格——顶格的续行会被读成新的一行数据，用户分不清哪几行属于同一条流。
func (p trackPlan) hangingIndent() int {
	if len(p.columns) > 0 && p.columns[0].key == colPrefix {
		return p.indent + p.columns[0].width + trackColumnSepWidth
	}
	return p.indent + trackColumnSepWidth
}

// trackCells 是「列标识 → 单元格文本」。
type trackCells map[string]string

// trackCell 是待写入的一格：text 已经按列宽补齐/截断，sep 是写在它之前的列间距。
type trackCell struct {
	text string
	sep  int
}

// videoCells 把一条视频流摊成单元格。
func videoCells(prefix string, v entity.Video, pageDur int) trackCells {
	dur := trackDur(pageDur, v.Dur)
	size := v.Size
	if size <= 0 {
		// 播放接口不给 size 时按 时长 × 码率 估算（码率是 kbps，这里沿用上游的 1024）。
		size = float64(dur) * float64(v.Bandwidth) * 1024 / 8
	}
	return trackCells{
		colPrefix: prefix,
		colName:   v.Dfn,
		colRes:    v.Res,
		colCodecs: v.Codecs,
		colFPS:    v.FPS,
		colKbps:   fmt.Sprintf("%d kbps", v.Bandwidth),
		colSize:   "~" + util.FormatFileSize(size),
	}
}

// audioCells 把一条音频流摊成单元格。音频没有分辨率/编码/帧率三列，
// 编码名（mp4a.40.2 等）落在「清晰度」列；其余列留空以便与视频行对齐。
func audioCells(prefix string, a entity.Audio, pageDur int) trackCells {
	dur := trackDur(pageDur, a.Dur)
	return trackCells{
		colPrefix: prefix,
		colName:   a.Codecs,
		colKbps:   fmt.Sprintf("%d kbps", a.Bandwidth),
		colSize:   "~" + util.FormatFileSize(float64(dur)*float64(a.Bandwidth)*1024/8),
	}
}

// videoLines / audioLines 渲染一行（折行时返回多行，续行已带悬挂缩进）。
func (p trackPlan) videoLines(prefix string, v entity.Video, pageDur int) []planLine {
	return p.layoutCells(p.cellRow(videoCells(prefix, v, pageDur)))
}

func (p trackPlan) audioLines(prefix string, a entity.Audio, pageDur int) []planLine {
	return p.layoutCells(p.cellRow(audioCells(prefix, a, pageDur)))
}

// headerLines 渲染表头行：与数据行共用同一套列宽、同一个对齐规则（单一来源），
// 只是单元格换成标签——所以标签必然落在数据列上，不会各写一套列宽而错位。
//
// 标签本身放不下列宽时会被截断（加 …）：标签是编译期常量，tracklayout_width_test.go 里
// 有一条用例钉住「每个标签都放得进它那一列」——真放不下时宁可测试红，也不要表头悄悄错位。
func (p trackPlan) headerLines(labels map[string]string) []planLine {
	cells := make([]trackCell, 0, len(p.columns))
	for i, col := range p.columns {
		sep := 0
		if i > 0 {
			sep = trackColumnSepWidth
		}
		cells = append(cells, trackCell{text: fitTrackCell(labels[col.key], col), sep: sep})
	}
	return p.layoutCells(cells)
}

// urlLines 渲染 TTY 下的直链提示行：与数据行同一缩进，整行不超过终端宽度。
func (p trackPlan) urlLines(raw string) []planLine {
	const hint = "↳ "
	room := p.width - p.indent - DisplayWidth(hint)
	if room < 1 {
		room = 1
	}
	return []planLine{{indent: p.indent, text: hint + elideURL(raw, room)}}
}

// planLine 是排版算好的一行终端输出：缩进与正文分开带，打印方按各行自己的缩进写。
//
// 折行时续行的缩进（悬挂缩进）与首行不同，所以缩进必须跟着每一行走，而不是由打印方
// 统一加一次——统一加会出现「缩进加在已含缩进的行上」，整行直接超出终端宽度。
type planLine struct {
	indent int
	text   string
}

// planLines 把排版结果拼成「含缩进的完整行」字符串切片（供只想要字符串的用例；
// 打印路径请逐行写 planLine，见 PrintAllTracks——续行的缩进与首行不同）。
func planLines(l []planLine) []string {
	out := make([]string, 0, len(l))
	for _, ln := range l {
		out = append(out, strings.Repeat(" ", ln.indent)+ln.text)
	}
	return out
}

// cellRow 把单元格按当前保留的列补齐/截断。
func (p trackPlan) cellRow(cells trackCells) []trackCell {
	row := make([]trackCell, 0, len(p.columns))
	for i, col := range p.columns {
		sep := 0
		if i > 0 {
			sep = trackColumnSepWidth
		}
		row = append(row, trackCell{text: fitTrackCell(cells[col.key], col), sep: sep})
	}
	return row
}

// layoutCells 把一串单元格拼成终端行：每行（缩进 + 正文）不超过终端宽度。
//
// 放不下就折行，续行用 hangingIndent 对齐到第一列数据；单元格本身比剩余位置还宽时按剩余
// 宽度截断（加 …）——「每行 ≤ 终端宽度」是硬约束，宁可截断也不换行。
// 返回的每一行带自己的缩进（首行是 plan.indent，续行是悬挂缩进），打印方不再另外加缩进。
func (p trackPlan) layoutCells(cells []trackCell) []planLine {
	budget := p.width
	if budget <= 0 {
		budget = util.DefaultTerminalWidth
	}
	var out []planLine
	var b strings.Builder
	indent := p.indent
	if indent >= budget {
		indent = 0 // 缩进比终端还宽（理论上到不了）时顶格，免得整行都是空格
	}
	col := indent
	first := true
	for _, c := range cells {
		sep := 0
		if !first {
			sep = c.sep
		}
		w := DisplayWidth(c.text)
		if !first && col+sep+w > budget {
			out = append(out, planLine{indent: indent, text: strings.TrimRight(b.String(), " ")})
			indent = p.hangingIndent()
			if indent >= budget {
				indent = 0
			}
			b.Reset()
			col, sep, first = indent, 0, true
		}
		if room := budget - col - sep; w > room {
			c.text = ellipsizeDisplay(c.text, room)
			w = DisplayWidth(c.text)
		}
		if sep > 0 {
			b.WriteString(trackColumnSep)
			col += sep
		}
		b.WriteString(c.text)
		col += w
		first = false
	}
	out = append(out, planLine{indent: indent, text: strings.TrimRight(b.String(), " ")})
	return out
}

// fitTrackCell 把一个值放进列里：短了按对齐补空格，超过 maxWidth 才截断加 …。
//
// 为什么不是一律截断到列宽：少数内容给分数帧率（"30000/1001" 是 10 列，列宽 6）——旧行为
// 让它溢出（整行跟着变宽），保留这个行为比截成 "30000…" 有用得多；宽度预算按 maxWidth 算，
// 溢出因此仍在终端宽度之内。
func fitTrackCell(s string, col trackColumn) string {
	w := DisplayWidth(s)
	limit := col.maxWidth
	if limit < col.width {
		limit = col.width
	}
	if w > limit {
		s = ellipsizeDisplay(s, limit)
		w = DisplayWidth(s)
	}
	if w >= col.width {
		return s
	}
	if col.right {
		return strings.Repeat(" ", col.width-w) + s
	}
	return s + strings.Repeat(" ", col.width-w)
}

// truncateDisplay 按显示宽度截断字符串（不追加省略号），截断点不会切开多字节字符。
func truncateDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(s) <= width {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if used+rw > width {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// ellipsizeDisplay 把字符串压到 width 显示列以内，末尾追加 …（结果 ≤ width）。
func ellipsizeDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return truncateDisplay(s, width-1) + "…"
}

// tailDisplay 保留字符串的尾部（域名主体、文件名后缀都在尾部），前面加 …。
func tailDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	room := width - 1 // 留给前缀的那个 …
	runes := []rune(s)
	used, i := 0, len(runes)
	for i > 0 {
		rw := runeDisplayWidth(runes[i-1])
		if used+rw > room {
			break
		}
		used += rw
		i--
	}
	return "…" + string(runes[i:])
}

// elideURL 把一条直链压到 width 显示列以内，供 TTY 下的清单显示（管道里必须逐字节原样，
// 见 printStreamURL）：
//
//  1. 放得下就原样返回（短直链一个字符都不动）；
//  2. 否则整段丢掉查询串（几百字符的主体），末尾补 "?…" 说明它被省略过；
//  3. 还放不下就折叠路径中段：host + "/…/" + 文件名（文件名尾部比前导的路径 ID 更有辨识度）；
//  4. 连 host + 文件名都放不下：先截文件名的尾部，再截 host 的尾部（域名主体在尾部），
//     最后退回「整条截断 + …」——任何分支的结果都不超过 width。
func elideURL(raw string, width int) string {
	if raw == "" || width <= 0 {
		return ""
	}
	if DisplayWidth(raw) <= width {
		return raw
	}
	base, droppedQuery := raw, false
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base, droppedQuery = base[:i], true
	}
	if droppedQuery {
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
	if name == "" {
		name = path
	}
	if prefix := scheme + host + "/…/"; DisplayWidth(prefix) < width {
		if room := width - DisplayWidth(prefix); room >= 1 {
			if DisplayWidth(name) <= room {
				return prefix + name
			}
			return prefix + tailDisplay(name, room)
		}
	}
	if head := scheme + "/…/" + name; DisplayWidth(head)+1 < width {
		if room := width - DisplayWidth(head); room >= 1 {
			return scheme + tailDisplay(host, room) + "/…/" + name
		}
	}
	return ellipsizeDisplay(base, width)
}

// trackDur 取用于估算体积的时长：分P时长优先，缺失（0）时退回轨道自带时长（与改前同一规则）。
func trackDur(pageDur, trackDur int) int {
	if pageDur == 0 {
		return trackDur
	}
	return pageDur
}

// formatVideoTrackRow 用给定的行首前缀渲染视频行：清单的序号行与 PrintSelectedTrack 的
// "[视频]" 行共用它，两处的列位置因此严格一致。
//
// 宽度取当前终端（每次渲染取一次）。折行时（极端窄终端）多行用 \n 连接；打印路径请用
// trackPlan 的 videoLines——续行需要悬挂缩进，交给日志前缀会顶格。
func formatVideoTrackRow(prefix string, v entity.Video, pageDur int) string {
	return formatVideoTrackRowAt(util.TerminalWidth(), prefix, v, pageDur)
}

// formatVideoTrackRowAt 是指定终端宽度的版本（用例按 40/60/80/120/200 逐档验证排版）。
func formatVideoTrackRowAt(width int, prefix string, v entity.Video, pageDur int) string {
	return strings.Join(planLines(newTrackPlan(width).videoLines(prefix, v, pageDur)), "\n")
}

// formatVideoTrackLine 渲染流清单里的一条视频流（行首是序号）。
func formatVideoTrackLine(index int, v entity.Video, pageDur int) string {
	return formatVideoTrackLineAt(util.TerminalWidth(), index, v, pageDur)
}

// formatVideoTrackLineAt 是指定终端宽度的版本。
func formatVideoTrackLineAt(width, index int, v entity.Video, pageDur int) string {
	return formatVideoTrackRowAt(width, fmt.Sprintf("%d.", index), v, pageDur)
}

// formatAudioTrackRow 用给定的行首前缀渲染音频行（列几何与视频行一致，见 videoTrackColumns）。
func formatAudioTrackRow(prefix string, a entity.Audio, pageDur int) string {
	return formatAudioTrackRowAt(util.TerminalWidth(), prefix, a, pageDur)
}

// formatAudioTrackRowAt 是指定终端宽度的版本。
func formatAudioTrackRowAt(width int, prefix string, a entity.Audio, pageDur int) string {
	return strings.Join(planLines(newTrackPlan(width).audioLines(prefix, a, pageDur)), "\n")
}

// formatAudioTrackLine 渲染流清单里的一条音频流（行首是序号）。
func formatAudioTrackLine(index int, a entity.Audio, pageDur int) string {
	return formatAudioTrackLineAt(util.TerminalWidth(), index, a, pageDur)
}

// formatAudioTrackLineAt 是指定终端宽度的版本。
func formatAudioTrackLineAt(width, index int, a entity.Audio, pageDur int) string {
	return formatAudioTrackRowAt(width, fmt.Sprintf("%d.", index), a, pageDur)
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

// padDisplayLeft 在左侧补空格（右对齐的帧率/码率/体积列用）；超宽同样原样返回。
// 只服务于本包的流清单，暂不导出（需要右对齐的调用方自己按 DisplayWidth 算即可）。
func padDisplayLeft(s string, width int) string {
	if n := width - DisplayWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}
