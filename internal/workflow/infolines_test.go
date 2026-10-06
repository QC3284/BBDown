package workflow

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown-go/internal/config"
	"github.com/QC3284/BBDown-go/internal/download"
	"github.com/QC3284/BBDown-go/internal/entity"
	"github.com/QC3284/BBDown-go/internal/util"
)

// ---- v3 信息块（元信息 / 分P清单 / 字幕清单 / ⚠ 标记 / 档位菜单 / 确认行）----
//
// 全部是纯函数用例：数据显式注入（pageInfo / []entity.Video / []entity.Subtitle），
// 不联网、不解析，排版口径可以逐字钉住。

// sampleTracks 造一组有档位层次的视频流：8K(127) / 1080P 高码率(112, 两条编码) / 720P 高清(64)。
func sampleTracks() []entity.Video {
	return []entity.Video{
		{ID: "127", Dfn: "8K 超高清", Bandwidth: 20000, Res: "7680x4320", Codecs: "hevc", FPS: "60"},
		{ID: "112", Dfn: "1080P 高码率", Bandwidth: 5000, Res: "1920x1080", Codecs: "hevc", FPS: "60"},
		{ID: "112", Dfn: "1080P 高码率", Bandwidth: 3000, Res: "1920x1080", Codecs: "avc", FPS: "60"},
		{ID: "64", Dfn: "720P 高清", Bandwidth: 1500, Res: "1280x720", Codecs: "avc", FPS: "30"},
	}
}

func TestFormatClock(t *testing.T) {
	cases := map[int]string{
		0:    "0:00",
		59:   "0:59",
		2055: "34:15", // v3 图上的时长
		3600: "1:00:00",
		3661: "1:01:01",
		-5:   "0:00",
	}
	for seconds, want := range cases {
		if got := formatClock(seconds); got != want {
			t.Errorf("formatClock(%d) = %q, want %q", seconds, got, want)
		}
	}
}

// TestRenderMetaLine 逐字钉住 v3 ② 的元信息行，并钉住「缺哪段省哪段」的省略规则。
func TestRenderMetaLine(t *testing.T) {
	page := entity.Page{Index: 1, Aid: "2", Cid: "9", OwnerName: "某UP", Dur: 2055}
	vInfo := &entity.VInfo{Title: "字幕君交流场所", PubTime: 1758326400, PagesInfo: []entity.Page{page}}

	got := renderMetaLine(pageInfoOf(page, vInfo, 8))
	want := "UP主: 某UP · " + page.Bvid() + " (av2) · 发布 " + "2025-09-20" + " · 34:15 · 分P 8"
	if got != want {
		t.Errorf("元信息行 = %q, want %q", got, want)
	}

	// 单P、无发布时间、无 UP：只留 BV/av 一段，不出现空段与多余分隔符。
	bare := entity.Page{Index: 1, Aid: "170001"}
	if got := renderMetaLine(pageInfoOf(bare, &entity.VInfo{}, 1)); got != bare.Bvid()+" (av170001) · 分P 1" {
		t.Errorf("缺段时的元信息行 = %q", got)
	}
	// 全缺：空串（调用方不打这一行）。
	if got := renderMetaLine(pageInfo{}); got != "" {
		t.Errorf("全缺时应当返回空串，实际 %q", got)
	}
}

// TestRenderPageListLine 逐字钉住 v3 ③ 的分P清单：≤3 条展开、多出来的用「…共 N 个分P」收口、
// 单P 不打这一行。
func TestRenderPageListLine(t *testing.T) {
	pages := []entity.Page{
		{Index: 1, Title: "字幕君交流场所", Dur: 2055},
		{Index: 2, Title: "幕后花絮", Dur: 1682},
		{Index: 3, Title: "未公开片段", Dur: 760},
		{Index: 4, Title: "第四话", Dur: 600},
		{Index: 5, Title: "第五话", Dur: 600},
	}
	// 完整形态（不夹取）的逐字断言在**宽终端**下做：默认非 TTY 宽度是 80 列，
	// 这一组数据的完整行超过 80，会被 t42 的宽度治理按显示宽度夹取（80 列的行为由
	// TestRenderPageListLineAt80 单独逐字钉住）。
	restore := util.SetTerminalWidthForTest(200)
	want := "分P: P1 字幕君交流场所 34:15 · P2 幕后花絮 28:02 · P3 未公开片段 12:40 · …共 5 个分P"
	if got := renderPageListLine(pages); got != want {
		t.Errorf("分P清单 = %q, want %q", got, want)
	}

	// 恰好 3 条：全部展开，不带省略号。
	three := renderPageListLine(pages[:3])
	if strings.Contains(three, "…") || !strings.HasSuffix(three, "共 3 个分P") {
		t.Errorf("3 条时不该省略：%q", three)
	}
	restore()
	// 单P：不打这一行。
	if got := renderPageListLine(pages[:1]); got != "" {
		t.Errorf("单P 不该打分P清单，实际 %q", got)
	}
	if got := renderPageListLine(nil); got != "" {
		t.Errorf("空分P 列表不该打，实际 %q", got)
	}
	// 标题/时长缺失时不留空位。
	minimal := renderPageListLine([]entity.Page{{Index: 1}, {Index: 2, Title: "有标题"}})
	if minimal != "分P: P1 · P2 有标题 · 共 2 个分P" {
		t.Errorf("缺标题/时长时的分P清单 = %q", minimal)
	}
}

// TestRenderSubtitleLine 逐字钉住 v3 ④：中文变体带短名（简中/繁中），其余只写 Lan；
// 没有字幕时返回空串（调用方不打这一行）。
func TestRenderSubtitleLine(t *testing.T) {
	subs := []entity.Subtitle{{Lan: "zh-CN"}, {Lan: "zh-Hant"}, {Lan: "en-US"}}
	want := "字幕: zh-CN(简中) · zh-Hant(繁中) · en-US"
	if got := renderSubtitleLine(subs); got != want {
		t.Errorf("字幕清单 = %q, want %q", got, want)
	}
	if got := renderSubtitleLine(nil); got != "" {
		t.Errorf("没有字幕时应返回空串，实际 %q", got)
	}
	// 空 Lan 的条目不进清单；AI 字幕有专门短名。
	ai := renderSubtitleLine([]entity.Subtitle{{Lan: ""}, {Lan: "ai-Zh"}, {Lan: "ja"}})
	if ai != "字幕: ai-Zh(简中(AI)) · ja" {
		t.Errorf("AI/日文字幕清单 = %q", ai)
	}
}

// TestRenderDoviWarnings 逐字钉住 v3 ⑤ 的兼容性标记：杜比视界/HDR Vivid 流各一行、
// 普通流不产生任何行；有可换的普通流时给出序号建议，没有时退回 --compat 建议。
func TestRenderDoviWarnings(t *testing.T) {
	dovi := []entity.Video{
		{ID: "126", Dfn: "杜比视界", Res: "3840x2160"},
		{ID: "112", Dfn: "1080P 高码率", Res: "1920x1080"},
	}
	got := renderDoviWarnings(dovi, false)
	if len(got) != 1 {
		t.Fatalf("杜比视界流应当只有一条标记，实际 %v", got)
	}
	want := "⚠ 0 号流为杜比视界：需支持 DOVI 的播放器；要通用兼容可换 1 号或 --compat"
	if got[0] != want {
		t.Errorf("杜比视界标记 = %q, want %q", got[0], want)
	}

	// HDR Vivid(129) 同样要标，措辞用 HDR Vivid 自己的兼容条件（不是 DOVI）。
	hdr := renderDoviWarnings([]entity.Video{{ID: config.HDRVividID, Dfn: "HDR 真彩"}, {ID: "80", Dfn: "1080P 高清"}}, false)
	if len(hdr) != 1 || !strings.Contains(hdr[0], "HDR Vivid") || strings.Contains(hdr[0], "DOVI") {
		t.Errorf("HDR Vivid 标记不正确：%v", hdr)
	}
	if !strings.Contains(hdr[0], "可换 1 号或 --compat") {
		t.Errorf("HDR Vivid 标记应当给出可换的序号：%q", hdr[0])
	}

	// 普通流：一条都没有。
	if got := renderDoviWarnings(sampleTracks(), false); len(got) != 0 {
		t.Errorf("普通流不该出现 ⚠ 标记，实际 %v", got)
	}
	// 全是风险档（--compat 也救不了）：不说「可换 N 号」，改说 --compat。
	only := renderDoviWarnings([]entity.Video{{ID: "126", Dfn: "杜比视界"}}, false)
	if len(only) != 1 || !strings.Contains(only[0], "加 --compat 可自动避开") {
		t.Errorf("没有可换档时的标记 = %v", only)
	}
	if strings.Contains(only[0], "可换") {
		t.Errorf("没有可换档时不该建议换序号：%q", only[0])
	}
}

// TestRecommendedTier 钉住 ★ 推荐档：--compat 关=最高档；开=避开 HDR Vivid/杜比视界后的第一档；
// 全档都有风险时退回 0（与 filterCompatTracks 的「绝不清空」同规则）。
func TestRecommendedTier(t *testing.T) {
	tracks := sampleTracks()
	tiers := qualityTiers(tracks)
	if len(tiers) != 3 {
		t.Fatalf("档位聚合 = %d 档（8K / 1080P 高码率 / 720P），want 3", len(tiers))
	}
	if tiers[1].Name != "1080P 高码率" || len(tiers[1].Indexes) != 2 {
		t.Errorf("同一档的两条编码应当合成一档：%+v", tiers[1])
	}
	if got := recommendedTier(tiers, tracks, false); got != 0 {
		t.Errorf("默认推荐档 = %d, want 0（最高档）", got)
	}

	withDovi := append([]entity.Video{{ID: "126", Dfn: "杜比视界", Bandwidth: 30000}}, sampleTracks()...)
	withDoviTiers := qualityTiers(withDovi)
	if got := recommendedTier(withDoviTiers, withDovi, false); got != 0 {
		t.Errorf("默认（不带 --compat）仍推荐最高档，got %d", got)
	}
	if got := recommendedTier(withDoviTiers, withDovi, true); got != 1 {
		t.Errorf("--compat 应当跳过杜比视界那一档，got %d want 1", got)
	}

	// 只有风险档：退回 0（不能让用户没有可选档）。
	risky := []entity.Video{{ID: "126", Dfn: "杜比视界"}, {ID: config.HDRVividID, Dfn: "HDR 真彩"}}
	if got := recommendedTier(qualityTiers(risky), risky, true); got != 0 {
		t.Errorf("全是风险档时应当退回 0，got %d", got)
	}
}

// TestRenderTierMenu 钉住 v3 ① 的档位菜单：一行两档、★ 跟在推荐档后面、两列按显示宽度对齐。
func TestRenderTierMenu(t *testing.T) {
	tiers := qualityTiers(sampleTracks())
	lines := renderTierMenu(tiers, 1)
	if len(lines) != 2 {
		t.Fatalf("3 档应当排成 2 行，实际 %d 行：%v", len(lines), lines)
	}
	// 两列的起点由最宽的格子决定：第二列的起始显示列 = 最大格宽 + 间隔。
	cellWidth := download.DisplayWidth("1. 1080P 高码率 ★") + tierMenuCellGap
	wantFirst := download.PadDisplay("0. 8K 超高清", cellWidth) + "1. 1080P 高码率 ★"
	if lines[0] != wantFirst {
		t.Errorf("第一行 = %q, want %q", lines[0], wantFirst)
	}
	if lines[1] != "2. 720P 高清" {
		t.Errorf("末行（单数） = %q", lines[1])
	}
	if col := download.DisplayWidth(lines[0][:strings.Index(lines[0], "1. 1080P")]); col != cellWidth {
		t.Errorf("第二列起始列 = %d, want %d", col, cellWidth)
	}
	if !strings.Contains(lines[0], "★") {
		t.Errorf("推荐档要带 ★：%q", lines[0])
	}
	if got := renderTierMenu(tiers, 0); !strings.HasPrefix(got[0], "0. 8K 超高清 ★") {
		t.Errorf("推荐档为 0 时 ★ 应当跟在 8K 后面：%q", got[0])
	}
}

// TestRenderSelectionSummary 逐字钉住 v3 ⑥ 的确认行：视频+音频字节和 + 输出**文件名**。
//
// t42 的宽度治理（依 docs/cli-width-audit.md §3.3）：输出段只留文件名——目录前缀在多P 时
// 就是稿件标题（与标题行重复），而且是最长的一段（80 列 pty 实测 161 列）。
func TestRenderSelectionSummary(t *testing.T) {
	// 不夹取的形态在宽终端下断言（默认 80 列时文件名会被 t42 的宽度治理夹取，
	// 80 列的逐字行为在 TestInfoLinesAt80 里）。
	restore := util.SetTerminalWidthForTest(200)
	defer restore()
	video := entity.Video{ID: "112", Dfn: "1080P 高码率", Bandwidth: 5000, Dur: 2055}
	audio := entity.Audio{ID: "30280", Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 2055}
	got := renderSelectionSummary(&video, &audio, 2055, "/home/video/[4K] 字幕君交流场所 P1.mp4")
	wantSize := util.FormatFileSize(trackSize(0, 5000, 2055, 2055) + trackSize(0, 132, 2055, 2055))
	want := "预计总大小 ≈ " + wantSize + " · 输出: [4K] 字幕君交流场所 P1.mp4"
	if got != want {
		t.Errorf("确认行 = %q, want %q", got, want)
	}

	// 多P 的默认产物布局（播放列表目录 = 稿件标题）在旧口径下会重复一遍标题，这里只剩文件名。
	multi := renderSelectionSummary(&video, &audio, 2055, "《明日方舟：终末地》核心章节「丹青渡」版本PV/[P1]【中】《明日方舟：终末地》核心章节「丹青渡」版本PV.mp4")
	if strings.Contains(multi, "/") || !strings.Contains(multi, "输出: [P1]") {
		t.Errorf("输出段应当只留文件名：%q", multi)
	}

	// 只有路径（体积未知）：只说输出；都没有：空串。
	if got := renderSelectionSummary(nil, nil, 0, "out.mp4"); got != "输出: out.mp4" {
		t.Errorf("无体积时 = %q", got)
	}
	if got := renderSelectionSummary(nil, nil, 0, ""); got != "" {
		t.Errorf("全缺时应当返回空串，实际 %q", got)
	}
}

// TestInfoLinesAt80 是 t42 的宽度治理逐字用例：80 列 pty（非 TTY 的默认宽度也是 80）下，
// 元信息行 / 分P清单行 / 预计大小行都 ≤80 显示列，且识别项与尾部保留。
//
// 变异验证（任一夹取被撤掉都会红）：
//   - 撤掉 renderPageListLineAt 的标题夹取 → 分P清单行超过 80 → 断言红；
//   - 撤掉 renderMetaLineAt 的尾段丢弃 → 元信息行 116 列 → 断言红；
//   - 撤掉 renderSelectionSummaryAt 的文件名夹取（保留全路径）→ 预计大小行超宽 → 断言红。
func TestInfoLinesAt80(t *testing.T) {
	restore := util.SetTerminalWidthForTest(80)
	defer restore()

	// 真机实测里最宽的那条元信息行（t39：116 列）：80 列下必须丢掉尾段，
	// UP主 与 BV/av 保留 —— 逐字。
	meta := pageInfo{
		Owner:     "明日方舟终末地",
		Bvid:      "BV1NCHf6eE6j",
		Aid:       "117393082810649",
		PubTime:   1781000000,
		PageDur:   351,
		PageCount: 4,
	}
	metaLine := renderMetaLine(meta)
	// 丢尾段的粒度是「逐段丢到刚好放得下」：这条数据丢掉「分P 4」就够，日期与时长因此还在。
	const wantMeta = "UP主: 明日方舟终末地 · BV1NCHf6eE6j (av117393082810649) · 发布 2026-06-09 · 5:51"
	if metaLine != wantMeta {
		t.Errorf("80 列的元信息行 = %q, want %q", metaLine, wantMeta)
	}
	if w := download.DisplayWidth(metaLine); w > 80 {
		t.Errorf("元信息行 %d 列 > 80", w)
	}
	// 识别项必须在：UP主 与 BV(av) 不参与丢弃。
	for _, keep := range []string{"UP主: 明日方舟终末地", "BV1NCHf6eE6j", "(av117393082810649)"} {
		if !strings.Contains(metaLine, keep) {
			t.Errorf("元信息行丢了识别项 %q：%q", keep, metaLine)
		}
	}
	// 极窄/超长 UP主 名的兜底：夹取名字也不裁 BV。
	narrow := renderMetaLineAt(30, meta)
	if !strings.Contains(narrow, "BV1NCHf6eE6j") || download.DisplayWidth(narrow) > 30 {
		t.Errorf("只剩识别项时的兜底 = %q（应当 ≤30 列且保留 BV）", narrow)
	}

	// 分P清单（t39 实测 227 列）：每项标题按显示宽度夹取，尾部「…共 N 个分P」保留 —— 逐字。
	pages := []entity.Page{
		{Index: 1, Title: "【中】《明日方舟：终末地》核心章节「丹青渡」版本PV", Dur: 351},
		{Index: 2, Title: "【英】《明日方舟：终末地》核心章节「丹青渡」版本PV", Dur: 350},
		{Index: 3, Title: "【日】《明日方舟：终末地》核心章节「丹青渡」版本PV", Dur: 350},
		{Index: 4, Title: "【韩】《明日方舟：终末地》核心章节「丹青渡」版本PV", Dur: 349},
	}
	pageLine := renderPageListLine(pages)
	const wantPages = "分P: P1 【中】《… 5:51 · P2 【英】《… 5:50 · P3 【日】《… 5:50 · …共 4 个分P"
	if pageLine != wantPages {
		t.Errorf("80 列的分P清单 = %q, want %q", pageLine, wantPages)
	}
	if w := download.DisplayWidth(pageLine); w > 80 {
		t.Errorf("分P清单 %d 列 > 80", w)
	}
	if !strings.HasSuffix(pageLine, "…共 4 个分P") {
		t.Errorf("尾部「…共 N 个分P」必须保留：%q", pageLine)
	}

	// 预计大小行（t39 实测 161 列）：只留文件名 + 仍超宽时按显示宽度夹取；这一行带 28 列
	// 时间戳前缀（workflow.go 用 util.Log 打），所以正文预算只有 80-28 = 52 列 —— 逐字。
	video := entity.Video{ID: "112", Dfn: "1080P 高码率", Bandwidth: 5000, Dur: 351}
	audio := entity.Audio{ID: "30280", Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 351}
	sum := renderSelectionSummary(&video, &audio, 351,
		"《明日方舟：终末地》核心章节「丹青渡」版本PV/[P1]【中】《明日方舟：终末地》核心章节「丹青渡」版本PV.mp4")
	wantSize := util.FormatFileSize(trackSize(0, 5000, 351, 351) + trackSize(0, 132, 351, 351))
	want := "预计总大小 ≈ " + wantSize + " · 输出: [P1]【中】《明日方…"
	if sum != want {
		t.Errorf("80 列的预计大小行 = %q, want %q", sum, want)
	}
	if w := download.DisplayWidth(sum) + util.LogIndentWidth; w > 80 {
		t.Errorf("预计大小行（含 28 列时间戳）= %d 列 > 80", w)
	}

	// 打印机这一层：缩进按剩余空间收回，**整行（缩进 + 正文）≤ 80** 才是判据。
	if got := infoIndentFor(80, "UP主: 某UP"); got != util.LogIndentWidth {
		t.Errorf("短行的缩进应当保持 %d，实际 %d", util.LogIndentWidth, got)
	}
	long := strings.Repeat("字", 70) // 140 列：80 列终端下缩进必须收到 0
	if got := infoIndentFor(80, long); got != 0 {
		t.Errorf("超宽行的缩进应当收到 0，实际 %d", got)
	}
	if total := infoIndentFor(80, metaLine) + download.DisplayWidth(metaLine); total > 80 {
		t.Errorf("缩进 + 元信息行 = %d 列 > 80", total)
	}
	if total := infoIndentFor(80, pageLine) + download.DisplayWidth(pageLine); total > 80 {
		t.Errorf("缩进 + 分P清单 = %d 列 > 80", total)
	}
}

// TestProgressCursorFor 是 t16 修订 ⑦ 的游标口径：多P 才有 "P3/8"，单P 留空
// （下载层只渲染，见 internal/download 的 DownloadConfig.ProgressCursor）。
func TestProgressCursorFor(t *testing.T) {
	cases := []struct {
		index, count int
		want         string
	}{
		{1, 8, "P1/8"},
		{3, 8, "P3/8"},
		{1, 1, ""},
		{2, 1, ""},
		{1, 0, ""},
	}
	for _, c := range cases {
		if got := progressCursorFor(c.index, c.count); got != c.want {
			t.Errorf("progressCursorFor(%d, %d) = %q, want %q", c.index, c.count, got, c.want)
		}
	}
}

// TestEnabledSidecars 钉住「哪些侧车会被下载」与 downloadOnePage 里各自的下载条件一致：
// 状态行里出现 ✔ 的项必须是这次真的会下的项。
func TestEnabledSidecars(t *testing.T) {
	base := config.DefaultMyOption()
	base.SkipSubtitle = false
	base.SkipCover = false

	if got := strings.Join(enabledSidecars(base), ","); got != "字幕,封面" {
		t.Errorf("默认（字幕+封面）：%q", got)
	}
	danmaku := base
	danmaku.DownloadDanmaku = true
	if got := strings.Join(enabledSidecars(danmaku), ","); got != "字幕,弹幕,封面" {
		t.Errorf("开弹幕：%q", got)
	}
	subOnly := base
	subOnly.SubOnly = true
	if got := strings.Join(enabledSidecars(subOnly), ","); got != "字幕" {
		t.Errorf("--sub-only（封面不参与）：%q", got)
	}
	coverOnly := base
	coverOnly.CoverOnly = true
	if got := strings.Join(enabledSidecars(coverOnly), ","); got != "封面" {
		t.Errorf("--cover-only（字幕/弹幕不参与）：%q", got)
	}
	danmakuOnly := base
	danmakuOnly.DanmakuOnly = true
	if got := strings.Join(enabledSidecars(danmakuOnly), ","); got != "弹幕" {
		t.Errorf("--danmaku-only：%q", got)
	}
	none := base
	none.SkipSubtitle, none.SkipCover = true, true
	if got := enabledSidecars(none); len(got) != 0 {
		t.Errorf("全关时不该有侧车项：%v", got)
	}
	showInfo := base
	showInfo.OnlyShowInfo = true
	if got := enabledSidecars(showInfo); len(got) != 0 {
		t.Errorf("-I 不下载任何侧车：%v", got)
	}
}

// TestPrintSidecarLine 钉住工作流这一层的侧车行：字幕那一格由编排层按四态渲染
// （t36 ⑥：没有可下 →「—」、失败 →「✗」、下到一条 →「✔」），弹幕/封面仍取
// download.SidecarStatus 的成品行；顺序固定 字幕 · 弹幕 · 封面，未启用的项不出现，
// 一项都没启用时整行不打。
//
// 变异验证：把 subtitleSidecarCell 的 default 分支改成「字幕 ✔」（即回到修复前的行为：
// 没有可下也报 ✔）→ 本用例的「没有可下」断言红。
func TestPrintSidecarLine(t *testing.T) {
	full := New(config.DefaultMyOption(), nil)
	full.sidecar = download.NewSidecarStatus("弹幕", "封面")
	full.markSidecar("弹幕")
	full.markSidecar("封面")
	full.subtitleSidecar = subtitleSidecarDone
	out := captureStdout(t, func() { full.printSidecarLine() })
	if !strings.Contains(out, "侧车: 字幕 ✔ · 弹幕 ✔ · 封面 ✔") {
		t.Errorf("三项全完成时 = %q", out)
	}

	// 没有可下字幕（该稿件没有 / 接口拿不到）：绝不报 ✔ —— 用户会以为字幕已经下好。
	noSub := New(config.DefaultMyOption(), nil)
	noSub.sidecar = download.NewSidecarStatus("封面")
	noSub.markSidecar("封面")
	noSub.subtitleSidecar = subtitleSidecarNone
	out = captureStdout(t, func() { noSub.printSidecarLine() })
	if !strings.Contains(out, "侧车: 字幕 — · 封面 ✔") {
		t.Errorf("没有可下字幕时 = %q，期望「字幕 —」", out)
	}
	if strings.Contains(out, "字幕 ✔") {
		t.Errorf("没有可下字幕时不该出现 字幕 ✔：%q", out)
	}

	// 有可下但一条都没下成：✗（失败是有用信号，不能淹没成成功）。
	failed := New(config.DefaultMyOption(), nil)
	failed.sidecar = download.NewSidecarStatus()
	failed.subtitleSidecar = subtitleSidecarFailed
	if out := captureStdout(t, func() { failed.printSidecarLine() }); !strings.Contains(out, "侧车: 字幕 ✗") {
		t.Errorf("字幕下载失败时 = %q，期望「字幕 ✗」", out)
	}

	// 只开封面：字幕未启用 → 这一项不出现（用户没要的东西不该占一行）。
	onlyCover := New(config.DefaultMyOption(), nil)
	onlyCover.Cfg.SubOnly = true // enabledSidecars：--sub-only 时封面不参与
	onlyCover.Cfg.SkipSubtitle = true
	onlyCover.Cfg.SkipCover = false
	onlyCover.sidecar = download.NewSidecarStatus("封面")
	onlyCover.markSidecar("封面")
	onlyCover.subtitleSidecar = subtitleSidecarDone
	out = captureStdout(t, func() { onlyCover.printSidecarLine() })
	if !strings.Contains(out, "侧车: 封面 ✔") || strings.Contains(out, "字幕") {
		t.Errorf("字幕未启用时 = %q", out)
	}

	// 一项都没启用（关掉字幕与封面，也没开弹幕）：整行不打。
	none := New(config.DefaultMyOption(), nil)
	none.Cfg.SkipSubtitle = true
	none.Cfg.SkipCover = true
	none.sidecar = download.NewSidecarStatus()
	if out := captureStdout(t, func() { none.printSidecarLine() }); out != "" {
		t.Errorf("一项都没启用时不该打状态行，实际 %q", out)
	}

	// 字幕启用但还没走到字幕阶段：按「—」（还没有产物），不是 ✔。
	pending := New(config.DefaultMyOption(), nil)
	pending.sidecar = download.NewSidecarStatus()
	if out := captureStdout(t, func() { pending.printSidecarLine() }); !strings.Contains(out, "侧车: 字幕 —") {
		t.Errorf("未走到字幕阶段时 = %q，期望「字幕 —」", out)
	}
}
