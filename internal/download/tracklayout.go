package download

import (
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// 流清单的排版：把「清晰度/分辨率/编码/帧率/码率/体积」排成固定列。
//
// 为什么列宽要按**显示列**而不是 rune 数：清单里的标签是中英混排（"1080P 高清"、"8K 超高清"、
// "[视频]"）。fmt 的 "%-12s" 按 rune 补空格，"1080P 高清"（8 rune / 10 列）与 "8K 超高清"
// （7 rune / 9 列）都会补到 12 个 rune，但终端里占 14 与 13 列——列永远对不齐。
// 所以列宽一律走 DisplayWidth/PadDisplay（中文/全角 2 列、ASCII 1 列）。
//
// 为什么是固定列宽而不是按本次清单取最大值：formatVideoTrackLine/formatAudioTrackLine 是
// 单行纯函数，PrintSelectedTrack 也只拿到单条轨道；动态取宽要么改签名，要么让同一行在不同
// 调用点宽度不同。固定列宽的代价是超宽值（如 durl 路径的长 codecs 串）会让该行变宽——
// 宁可这一行不齐，也不截断信息。
const (
	trackPrefixWidth = 7  // 行首：序号 "0."，或选中轨道的标签 "[视频]"/"[音频]"（6 列 + 1 空格）
	trackNameWidth   = 12 // 清晰度（视频）/ 编码名（音频）；QualityMap 里最长是 "1080P 高帧率"（12 列）
	trackResWidth    = 10 // 分辨率
	trackCodecsWidth = 7  // 视频编码：AVC/HEVC/AV1/UNKNOWN（兜底名恰好 7 列）
	trackFPSWidth    = 3  // 帧率，右对齐
	trackKbpsWidth   = 10 // 码率，右对齐
	trackSizeWidth   = 11 // 体积，右对齐
)

// trackColumnSep 是列间分隔（两个空格）。所有行共用同一个分隔，右对齐列才有稳定的右边缘。
const trackColumnSep = "  "

// trackRow 是流清单里的一行。视频行六列全填；音频行没有分辨率/编码/帧率，留空占位——
// 正是这些占位让码率与体积两列在视频段与音频段之间也对齐。
type trackRow struct {
	prefix string
	name   string
	res    string
	codecs string
	fps    string
	kbps   int64
	size   float64 // 字节
}

// render 按固定列宽拼出一行。除超宽值外，任何两行的显示宽度都相同。
func (r trackRow) render() string {
	var b strings.Builder
	b.WriteString(PadDisplay(r.prefix, trackPrefixWidth))
	b.WriteString(PadDisplay(r.name, trackNameWidth))
	b.WriteString(trackColumnSep)
	b.WriteString(PadDisplay(r.res, trackResWidth))
	b.WriteString(trackColumnSep)
	b.WriteString(PadDisplay(r.codecs, trackCodecsWidth))
	b.WriteString(trackColumnSep)
	b.WriteString(padDisplayLeft(r.fps, trackFPSWidth))
	b.WriteString(trackColumnSep)
	b.WriteString(padDisplayLeft(fmt.Sprintf("%d kbps", r.kbps), trackKbpsWidth))
	b.WriteString(trackColumnSep)
	b.WriteString(padDisplayLeft("~"+util.FormatFileSize(r.size), trackSizeWidth))
	return b.String()
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
func formatVideoTrackRow(prefix string, v entity.Video, pageDur int) string {
	dur := trackDur(pageDur, v.Dur)
	size := v.Size
	if size <= 0 {
		// 播放接口不给 size 时按 时长 × 码率 估算（码率是 kbps，这里沿用上游的 1024）。
		size = float64(dur) * float64(v.Bandwidth) * 1024 / 8
	}
	return trackRow{
		prefix: prefix,
		name:   v.Dfn,
		res:    v.Res,
		codecs: v.Codecs,
		fps:    v.FPS,
		kbps:   v.Bandwidth,
		size:   size,
	}.render()
}

// formatVideoTrackLine 渲染流清单里的一条视频流（行首是序号）。
func formatVideoTrackLine(index int, v entity.Video, pageDur int) string {
	return formatVideoTrackRow(fmt.Sprintf("%d.", index), v, pageDur)
}

// formatAudioTrackRow 用给定的行首前缀渲染音频行。音频没有分辨率/编码/帧率三列，
// 编码名（mp4a.40.2 等）落在「清晰度」列；其余列留空以便与视频行对齐。
func formatAudioTrackRow(prefix string, a entity.Audio, pageDur int) string {
	dur := trackDur(pageDur, a.Dur)
	return trackRow{
		prefix: prefix,
		name:   a.Codecs,
		kbps:   a.Bandwidth,
		size:   float64(dur) * float64(a.Bandwidth) * 1024 / 8,
	}.render()
}

// formatAudioTrackLine 渲染流清单里的一条音频流（行首是序号）。
func formatAudioTrackLine(index int, a entity.Audio, pageDur int) string {
	return formatAudioTrackRow(fmt.Sprintf("%d.", index), a, pageDur)
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
