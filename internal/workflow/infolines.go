package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// ---- BBDownT v3 口径的信息块 ----
//
// 这块输出（元信息 / 分P清单 / 档位菜单 / ⚠ 标记 / 字幕清单 / 选中确认行）与既有「任务卡」是两套
// 排版：前者按 v3 定稿（docs/cli-concept-bbdt-v3.png），后者是 2.13.0 的紧凑卡片。渲染全部走这里
// 的纯函数——**数据是显式传进来的**（pageInfo / []entity.Video / []entity.Subtitle），
// 所以可以离线逐字断言，不必为了看一眼排版去联网解析。
//
// 与机器契约的关系：-I / --print-urls / --info-json 有自己的输出（脚本按行取数据），
// 这些行只在**真实下载路径**打印（见 downloadOnePage 里的 humanInfo 判定），
// 机器模式下 stdout 逐字节不变。

// pageInfo 是「元信息行 + 分P清单行」的纯数据输入。
type pageInfo struct {
	Title     string // 稿件标题（标题行由 printVideoHeader 单独打，这里只用于兜底）
	Owner     string // UP 主名
	Bvid      string // BV 号（空则不打 BV 段）
	Aid       string // av 号（与 BV 一起写成 "BV… (avN)"）
	PubTime   int64  // 发布时间戳（0 = 未知，整段省略）
	PageDur   int    // 当前分P时长（秒；0 = 未知，整段省略）
	PageCount int    // 稿件分P总数
	Pages     []entity.Page
}

const (
	// maxPageListEntries 是分P清单最多展开的条目数（v3：≤3 条，其余用「…共 N 个分P」收口）。
	maxPageListEntries = 3
	// tierMenuCellGap 是档位菜单两列之间的最小间隔（列宽按最宽的格子算，见 renderTierMenu）。
	tierMenuCellGap = 2
)

// formatClock 把秒写成 h:mm:ss（不足 1 小时是 m:ss）——v3 的时长口径。
//
// 不复用 util.FormatTime(sec, true)：那是上游的 HH:MM:SS（恒定两位小时），
// v3 图上「34:15」不带前导零、也不显示 0 小时。
func formatClock(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// renderMetaLine 组装元信息行（v3 ②）：
//
//	UP主: 某UP · BV1xx411c7mD (av2) · 发布 2026-09-20 · 34:15 · 分P 8
//
// 每一段都来自显式数据，缺哪段就省哪段（全缺时返回空串，调用方不打这一行）。
func renderMetaLine(i pageInfo) string {
	var parts []string
	if i.Owner != "" {
		parts = append(parts, "UP主: "+i.Owner)
	}
	switch {
	case i.Bvid != "" && i.Aid != "":
		parts = append(parts, fmt.Sprintf("%s (av%s)", i.Bvid, i.Aid))
	case i.Bvid != "":
		parts = append(parts, i.Bvid)
	case i.Aid != "":
		parts = append(parts, fmt.Sprintf("av%s", i.Aid))
	}
	if i.PubTime > 0 {
		parts = append(parts, "发布 "+time.Unix(i.PubTime, 0).Format("2006-01-02"))
	}
	if i.PageDur > 0 {
		parts = append(parts, formatClock(i.PageDur))
	}
	if i.PageCount > 0 {
		parts = append(parts, fmt.Sprintf("分P %d", i.PageCount))
	}
	return strings.Join(parts, " · ")
}

// renderPageListLine 组装分P清单行（v3 ③）：
//
//	分P: P1 第一话 34:15 · P2 第二话 28:02 · P3 第三话 12:40 · …共 8 个分P
//
// 单P 稿件不打这一行（v3：只对多P 展示）。展开条目数上限 maxPageListEntries，
// 被截断时在「共 N 个分P」前加省略号——用户一眼能看出还有没列出的分P。
func renderPageListLine(pages []entity.Page) string {
	if len(pages) <= 1 {
		return ""
	}
	shown := pages
	truncated := false
	if len(shown) > maxPageListEntries {
		shown = shown[:maxPageListEntries]
		truncated = true
	}
	parts := make([]string, 0, len(shown)+1)
	for _, p := range shown {
		entry := fmt.Sprintf("P%d", p.Index)
		if p.Title != "" {
			entry += " " + p.Title
		}
		if p.Dur > 0 {
			entry += " " + formatClock(p.Dur)
		}
		parts = append(parts, entry)
	}
	tail := fmt.Sprintf("共 %d 个分P", len(pages))
	if truncated {
		tail = "…" + tail
	}
	parts = append(parts, tail)
	return "分P: " + strings.Join(parts, " · ")
}

// subtitleShortNames 是字幕清单里那个括号短名（v3 ④）：只给「光看 zh-CN / zh-Hant 分不清简体
// 还是繁体」的语言与 AI 字幕，其余只写 Lan（v3 图上的 en-US 就没有括号）。
var subtitleShortNames = map[string]string{
	"zh-CN":   "简中",
	"zh-Hans": "简中",
	"zh-Hant": "繁中",
	"zh-TW":   "繁中",
	"zh-HK":   "港繁",
	"ai-Zh":   "简中(AI)",
	"ai-En":   "英文(AI)",
}

// renderSubtitleLine 组装字幕清单行（v3 ④）：字幕: zh-CN(简中) · zh-Hant(繁中) · en-US
// 没有字幕时返回空串（调用方不打这一行）。
func renderSubtitleLine(subs []entity.Subtitle) string {
	parts := make([]string, 0, len(subs))
	for _, s := range subs {
		if s.Lan == "" {
			continue
		}
		if short, ok := subtitleShortNames[s.Lan]; ok {
			parts = append(parts, s.Lan+"("+short+")")
			continue
		}
		parts = append(parts, s.Lan)
	}
	if len(parts) == 0 {
		return ""
	}
	return "字幕: " + strings.Join(parts, " · ")
}

// hdrStreamLabel 给「本机/多数播放器可能播不了」的档位一个名字；普通档位返回空串。
func hdrStreamLabel(v entity.Video) string {
	switch {
	case v.ID == config.HDRVividID:
		return "HDR Vivid"
	case v.ID == "126" || config.QualityMap[v.ID] == "杜比视界":
		return "杜比视界"
	}
	return ""
}

// renderDoviWarnings 组装流表后的 ⚠ 标记（v3 ⑤）：每个有兼容性风险的流一行，普通流不产生任何行。
//
//	⚠ 0 号流为杜比视界：需支持 DOVI 的播放器；要通用兼容可换 1 号或 --compat
//
// 「可换 M 号」里的 M 是同一份清单里第一条没有风险的流（没有就不提这条建议）——
// 用户照着数字换即可，不必自己找哪条能播。
func renderDoviWarnings(tracks []entity.Video, compat bool) []string {
	alternative := -1
	for i, v := range tracks {
		if hdrStreamLabel(v) == "" {
			alternative = i
			break
		}
	}
	var out []string
	for i, v := range tracks {
		label := hdrStreamLabel(v)
		if label == "" {
			continue
		}
		line := fmt.Sprintf("⚠ %d 号流为%s", i, label)
		if label == "杜比视界" {
			line += "：需支持 DOVI 的播放器"
		} else {
			line += "：需要支持 HDR Vivid 的设备与播放器"
		}
		switch {
		case alternative >= 0 && alternative != i:
			line += fmt.Sprintf("；要通用兼容可换 %d 号或 --compat", alternative)
		case !compat:
			line += "；加 --compat 可自动避开"
		}
		out = append(out, line)
	}
	return out
}

// qualityTier 是一档清晰度：接口会把同一档的不同编码（HEVC / AVC）各给一条流，
// v3 的档位菜单按**档**（dfn 显示名）聚合，用户选档之后才在档内选流。
type qualityTier struct {
	Name    string // dfn 显示名
	Indexes []int  // 这些流在 result.VideoTracks 里的下标（保持接口顺序）
}

// qualityTiers 按 dfn 显示名把视频流聚合成档位（保持首次出现的顺序）。
func qualityTiers(tracks []entity.Video) []qualityTier {
	var tiers []qualityTier
	pos := make(map[string]int, len(tracks))
	for i, v := range tracks {
		name := v.Dfn
		if name == "" {
			name = config.QualityMap[v.ID]
		}
		if at, ok := pos[name]; ok {
			tiers[at].Indexes = append(tiers[at].Indexes, i)
			continue
		}
		pos[name] = len(tiers)
		tiers = append(tiers, qualityTier{Name: name, Indexes: []int{i}})
	}
	return tiers
}

// recommendedTier 返回 ★ 推荐档的下标：未开 --compat 时是最高的第一档；
// 开了 --compat 时是「避开 HDR Vivid / 杜比视界后的第一档」。
//
// 全档都有风险时退回 0（与 filterCompatTracks「绝不清空候选」同一条规则：
// 不能把用户逼到没有可选档）。
func recommendedTier(tiers []qualityTier, tracks []entity.Video, compat bool) int {
	if len(tiers) == 0 {
		return 0
	}
	if !compat {
		return 0
	}
	for i, tier := range tiers {
		if len(tier.Indexes) == 0 {
			continue
		}
		if hdrStreamLabel(tracks[tier.Indexes[0]]) == "" {
			return i
		}
	}
	return 0
}

// renderTierMenu 渲染档位菜单（v3 ①：一行两档，★ 标推荐档）：
//
//  0. 8K 超高清      1. 4K 超清
//  2. 1080P 高码率 ★ 3. 1080P 高清
//
// 列宽按**显示宽度**（中文算 2 列）取最宽的格子 + 间隔，★ 计入格子宽度，两列因此始终对齐。
func renderTierMenu(tiers []qualityTier, recommended int) []string {
	cells := make([]string, len(tiers))
	width := 0
	for i, tier := range tiers {
		cells[i] = fmt.Sprintf("%d. %s", i, tier.Name)
		if i == recommended {
			cells[i] += " ★"
		}
		if w := download.DisplayWidth(cells[i]); w > width {
			width = w
		}
	}
	width += tierMenuCellGap
	var lines []string
	for i := 0; i < len(cells); i += 2 {
		if i+1 < len(cells) {
			lines = append(lines, download.PadDisplay(cells[i], width)+cells[i+1])
			continue
		}
		lines = append(lines, cells[i])
	}
	return lines
}

// renderSelectionSummary 组装选中确认行（v3 ⑥）：
//
//	预计总大小 ≈ 252.33 MB · 输出: /home/video/[4K] 标题 P1.mp4
//
// 大小是所选视频 + 音频的字节和（与流表同一套体积口径：声明体积优先，否则按码率×时长估算）。
// 两条信息都可能缺失，缺哪条省哪条；都缺时返回空串。
func renderSelectionSummary(video *entity.Video, audio *entity.Audio, pageDur int, savePath string) string {
	var size float64
	if video != nil {
		size += trackSize(video.Size, video.Bandwidth, pageDur, video.Dur)
	}
	if audio != nil {
		size += trackSize(0, audio.Bandwidth, pageDur, audio.Dur)
	}
	var parts []string
	if size > 0 {
		parts = append(parts, "预计总大小 ≈ "+util.FormatFileSize(size))
	}
	if savePath != "" {
		parts = append(parts, "输出: "+savePath)
	}
	return strings.Join(parts, " · ")
}

// progressCursorFor 给出进度行行首的分P游标（t15 的 DownloadConfig.ProgressCursor 口径）：
// 多P 时是 "P3/8"，单P 返回空串（单P 不需要游标，v3 的进度行里也不该多一段）。
func progressCursorFor(pageIndex, pageCount int) string {
	if pageCount <= 1 {
		return ""
	}
	return fmt.Sprintf("P%d/%d", pageIndex, pageCount)
}

// enabledSidecars 按配置给出本次运行启用的侧车项（顺序由 download 层的固定顺序决定）。
// 判定条件与 downloadOnePage 里各自的下载条件**逐条对应**：显示状态行的那几项必须真的会下，
// 否则会出现「侧车行里有封面 ✔，但这次根本没开封面」这种自相矛盾的行。
func enabledSidecars(cfg config.MyOption) []string {
	var out []string
	if !cfg.SkipSubtitle && !cfg.DanmakuOnly && !cfg.CoverOnly && !cfg.OnlyShowInfo {
		out = append(out, "字幕")
	}
	// 弹幕阶段在 --cover-only 的提前 return 之后，那一路不会跑到它。
	if (cfg.DownloadDanmaku || cfg.DanmakuOnly) && !cfg.OnlyShowInfo && !cfg.CoverOnly {
		out = append(out, "弹幕")
	}
	// 封面有两条路：常规路径（排除 CoverOnly）与 --cover-only 自己的分支（不看 SkipCover）。
	// 两者合起来就是：不是 --sub-only/--danmaku-only，且（开着封面 或 就是要封面）。
	if !cfg.SubOnly && !cfg.DanmakuOnly && (!cfg.SkipCover || cfg.CoverOnly) && !cfg.OnlyShowInfo {
		out = append(out, "封面")
	}
	return out
}

// printInfoLines 打印元信息行与分P清单行（无时间戳、按 28 列缩进对齐，与流表同一条通道）。
func printInfoLines(i pageInfo) {
	if line := renderMetaLine(i); line != "" {
		util.LogColorNoTimeIndent(util.LogIndentWidth, "%s", line)
	}
	if line := renderPageListLine(i.Pages); line != "" {
		util.LogColorNoTimeIndent(util.LogIndentWidth, "%s", line)
	}
}

// chooseTierInteractive 是 -i 的档位选择（v3 ①）：列档位菜单（★ 推荐档）→ 读序号 →
// 返回该档的流在 result.VideoTracks 里的下标。ok=false 表示用户中断（调用方必须中止，
// 不能当成「选了推荐档」继续下载——与既有 readIntSafe 的取消语义一致）。
//
// 这里**不发任何请求**：档位菜单与档内流表都来自已经拿到的 ParsedResult（零额外 fetch）。
func (w *Workflow) chooseTierInteractive(ctx context.Context, tiers []qualityTier, tracks []entity.Video) ([]int, bool) {
	recommended := recommendedTier(tiers, tracks, w.Cfg.Compat)
	util.Log("该稿件共 %d 档清晰度:", len(tiers))
	for _, line := range renderTierMenu(tiers, recommended) {
		util.LogColorNoTimeIndent(util.LogIndentWidth, "%s", line)
	}
	fmt.Print("请选择最想要的清晰度(输入序号): ")
	fmt.Print(util.AnsiCyan)
	idx, ok := readIntSafe(ctx)
	fmt.Print(util.AnsiReset)
	if !ok {
		return nil, false
	}
	if idx < 0 || idx >= len(tiers) {
		idx = recommended
	}
	return tiers[idx].Indexes, true
}

// tierResult 复制一份只含所选档视频流的解析结果（音频等字段原样），供「只显示该档流表」用。
func tierResult(result *entity.ParsedResult, indexes []int) *entity.ParsedResult {
	filtered := *result
	filtered.VideoTracks = make([]entity.Video, 0, len(indexes))
	for _, i := range indexes {
		if i >= 0 && i < len(result.VideoTracks) {
			filtered.VideoTracks = append(filtered.VideoTracks, result.VideoTracks[i])
		}
	}
	return &filtered
}

// subtitlesFor 取本页字幕清单并缓存：显示阶段（v3 的「字幕: …」行）与下载阶段共用同一次请求。
// 未启用字幕时返回 nil 且**不发请求**（与 enabledSidecars 的字幕条件逐条对应）。
func (w *Workflow) subtitlesFor(ctx context.Context, page entity.Page) []entity.Subtitle {
	if w.Cfg.SkipSubtitle || w.Cfg.DanmakuOnly || w.Cfg.CoverOnly || w.Cfg.OnlyShowInfo {
		return nil
	}
	if w.subsCache == nil {
		w.subsCache = make(map[string][]entity.Subtitle)
	}
	if subs, ok := w.subsCache[page.Cid]; ok {
		return subs
	}
	subs, _ := util.GetSubtitles(ctx, w.HTTPClient, page.Aid, page.Cid, page.Epid, page.Index, w.Cfg.UseIntlAPI, w.Cfg.Cookie)
	w.subsCache[page.Cid] = subs
	return subs
}

// markSidecar 记一项侧车完成（未启用/未知标签由 download.SidecarStatus 自己忽略）。
func (w *Workflow) markSidecar(label string) { w.sidecar.MarkDone(label) }

// printSidecarLine 打印侧车状态行；一项都没启用时整行不打（Line 返回空串）。
func (w *Workflow) printSidecarLine() {
	if w.sidecar == nil {
		return
	}
	if line := w.sidecar.Line(); line != "" {
		util.LogColorNoTimeIndent(util.LogIndentWidth, "%s", line)
	}
}

// pageInfoOf 用当前分P与稿件信息填 pageInfo。
func pageInfoOf(page entity.Page, vInfo *entity.VInfo, pageCount int) pageInfo {
	return pageInfo{
		Title:     vInfo.Title,
		Owner:     page.OwnerName,
		Bvid:      page.Bvid(),
		Aid:       page.Aid,
		PubTime:   vInfo.PubTime,
		PageDur:   page.Dur,
		PageCount: pageCount,
		Pages:     vInfo.PagesInfo,
	}
}
