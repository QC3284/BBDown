package workflow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// taskCard 是「解析完成、开始下载前」那块紧凑信息卡的纯数据输入。
//
// 排版规则（字段缺失省略该行、标签按**显示宽度**对齐）全部放在纯函数 renderTaskCard 里：
// 调用点要经过整条解析/选流管线才走得到，而排版本身与管线无关，抽出来才能单测。
type taskCard struct {
	Title      string // 稿件标题（含 [试看] 之类已经生效的改动）
	Bvid       string // BV 号
	Owner      string // 来源 UP
	Page       string // 分P 描述，如 "P2/共12P 第二话"（单P 稿件留空）
	Video      string // 视频流一行
	Audio      string // 音频流一行
	OutputPath string // 输出路径
}

// 标签列宽度取候选标签里最宽的「输出路径」（4 个全角字符 = 8 显示列）。
// 用固定值而不是「本次出现过的标签的最大宽度」：某些行被省略时，其余行的值列才不会整体左移。
const (
	taskCardHeader     = "任务卡"
	taskCardIndent     = "  "
	taskCardLabelWidth = 8
	taskCardGap        = 2
)

// renderTaskCard 渲染任务卡：每个存在的字段一行，标签列对齐，值从固定的显示列开始。
//
// 返回整块文本（含末尾换行）。字段为空即整行省略——没有音频流、没有 BV、单P 稿件都不留空标签行。
func renderTaskCard(c taskCard) string {
	rows := []struct{ label, value string }{
		{"标题", c.Title},
		{"BV", c.Bvid},
		{"UP", c.Owner},
		{"分P", c.Page},
		{"视频流", c.Video},
		{"音频流", c.Audio},
		{"输出路径", c.OutputPath},
	}
	var b strings.Builder
	b.WriteString(taskCardHeader)
	b.WriteByte('\n')
	for _, r := range rows {
		if r.value == "" {
			continue
		}
		b.WriteString(taskCardIndent)
		// 补白复用流清单排版的同一套规则（download.PadDisplay）：全角标签算 2 显示列，
		// 卡片与流清单不会各按各的口径对齐。
		b.WriteString(download.PadDisplay(r.label, taskCardLabelWidth))
		b.WriteString(strings.Repeat(" ", taskCardGap))
		// 值来自接口（标题/UP 名/路径都是服务端可控文本），而卡片是直接写终端的：
		// 这里补上日志同款的清洗，免得标题里的换行或 ANSI 序列伪造出一行卡片（上游 RF-54/RF-70）。
		b.WriteString(util.SanitizeLogString(r.value))
		b.WriteByte('\n')
	}
	return b.String()
}

// printTaskCard 把卡片原样写到控制台。
//
// 用 Logger.Printf 而不是 util.Log：Log 会按「单行日志」清洗参数（控制字符变空格、连续空白折叠），
// 多行且靠空白对齐的卡片会被压成一行。Printf 走同一个 util.ConsoleLock 与「先给进度行收尾」的
// 约定（internal/util/logger.go 的 consoleWrite），所以与日志/进度条不会互相插行。
func printTaskCard(c taskCard) {
	util.GetLogger().Printf("%s", renderTaskCard(c))
}

// buildTaskCard 用本页的解析结果填卡片数据；没有的字段留空，由 renderTaskCard 省略那一行。
func (w *Workflow) buildTaskCard(page entity.Page, pagesCount int, title string,
	video *entity.Video, audio *entity.Audio, savePath string) taskCard {
	card := taskCard{
		Title:      title,
		Bvid:       page.Bvid(),
		Owner:      page.OwnerName,
		OutputPath: savePath,
	}
	if pagesCount > 1 {
		card.Page = fmt.Sprintf("P%d/共%dP", page.Index, pagesCount)
		// 分P 标题与主标题重复时不再贴一遍（单P 稿件的 page.Title 常常就是主标题）。
		if page.Title != "" && page.Title != title {
			card.Page += " " + page.Title
		}
	}
	if !w.Cfg.HideStreams {
		// --hide-streams 的契约是「不打印流清单」：卡片里这两行同样省略（其余行照旧对齐）。
		card.Video = describeVideoTrack(video, page.Dur)
		card.Audio = describeAudioTrack(audio, page.Dur)
	}
	return card
}

// describeVideoTrack 把一条视频流压成一行：画质 · 分辨率 · 帧率 · 编码 · 码率 · 预估体积。
// 空字段直接跳过（与上游 Display 的 "[] " 处理同效），nil 返回空串（整行省略）。
func describeVideoTrack(v *entity.Video, pageDur int) string {
	if v == nil {
		return ""
	}
	var parts []string
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	add(v.Dfn)
	add(v.Res)
	add(formatFPS(v.FPS))
	add(v.Codecs)
	if v.Bandwidth > 0 {
		add(fmt.Sprintf("%d kbps", v.Bandwidth))
	}
	if size := trackSize(v.Size, v.Bandwidth, pageDur, v.Dur); size > 0 {
		add("~" + util.FormatFileSize(size))
	}
	return strings.Join(parts, " · ")
}

// describeAudioTrack 把一条音频流压成一行：编码 · 码率 · 预估体积
// （与 download.PrintSelectedTrack 的音频行取同一组字段，不额外塞音频 id）。
func describeAudioTrack(a *entity.Audio, pageDur int) string {
	if a == nil {
		return ""
	}
	var parts []string
	if a.Codecs != "" {
		parts = append(parts, a.Codecs)
	}
	if a.Bandwidth > 0 {
		parts = append(parts, fmt.Sprintf("%d kbps", a.Bandwidth))
	}
	if size := trackSize(0, a.Bandwidth, pageDur, a.Dur); size > 0 {
		parts = append(parts, "~"+util.FormatFileSize(size))
	}
	return strings.Join(parts, " · ")
}

// trackSize 是轨道体积的既有口径（download.PrintSelectedTrack 同源）：声明体积优先，
// 没有就按「时长 × 码率」估算——卡片里的体积因此与「已选择的流」那一行完全一致。
func trackSize(declared float64, bandwidth int64, pageDur, trackDur int) float64 {
	if declared > 0 {
		return declared
	}
	dur := pageDur
	if dur == 0 {
		dur = trackDur
	}
	return float64(dur) * float64(bandwidth) * 1024 / 8
}

// formatFPS 把接口里的帧率写成 "30fps"：playurl 有的端点给 "30"、有的给 "30.000"，
// 只加后缀会打出 "30.000fps"。非数值形式（如 "30000/1001"）原样保留。
func formatFPS(fps string) string {
	if fps == "" {
		return ""
	}
	if f, err := strconv.ParseFloat(fps, 64); err == nil && f > 0 {
		return strconv.FormatFloat(f, 'f', -1, 64) + "fps"
	}
	return fps
}
