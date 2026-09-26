package workflow

import (
	"regexp"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// ---- 纯函数：排版规则 ----

// 值列按**显示宽度**对齐：全角标签「输出路径」（8 显示列）与 ASCII 标签「BV」（2 显示列）
// 的值必须从同一列开始。
//
// 变异验证：把 renderTaskCard 里的 download.PadDisplay 换回按字节补白（len(label)），
// CJK 标签的补白消失，值列错位，本用例变红。
func TestRenderTaskCardAlignsValuesByDisplayWidth(t *testing.T) {
	out := renderTaskCard(taskCard{
		Title:      "示例稿件",
		Bvid:       "BV1xx411c7mD",
		Owner:      "某某UP",
		Page:       "P2/共12P 第二话",
		Video:      "1080P 高清 · 1920x1080 · 60fps · avc1.640032 · 2000 kbps · ~1.22 MB",
		Audio:      "M4A · 64 kbps · ~80.00 KB",
		OutputPath: "out/示例稿件.mp4",
	})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if lines[0] != taskCardHeader {
		t.Fatalf("首行应是卡片表头 %q，实际 %q", taskCardHeader, lines[0])
	}
	labels := []string{"标题", "BV", "UP", "分P", "视频流", "音频流", "输出路径"}
	if len(lines) != len(labels)+1 {
		t.Fatalf("应有表头 + %d 行，实际 %d 行：%q", len(labels), len(lines)-1, out)
	}

	col := -1
	for i, ln := range lines[1:] {
		label := labels[i]
		if !strings.HasPrefix(ln, taskCardIndent+label) {
			t.Fatalf("第 %d 行的标签不是 %q：%q", i+1, label, ln)
		}
		rest := ln[len(taskCardIndent)+len(label):] // label 是前缀，按字节切安全
		pad := len(rest) - len(strings.TrimLeft(rest, " "))
		got := download.DisplayWidth(taskCardIndent+label) + pad
		if col < 0 {
			col = got
			continue
		}
		if got != col {
			t.Errorf("值列没有对齐：%q 的值从第 %d 显示列开始，其余行是第 %d 列", ln, got, col)
		}
	}
}

// 字段缺失（没有 BV / 音频流 / 输出路径，或单P 稿件没有分P 行）时整行省略，不留「只有标签」的空行。
//
// 变异验证：把 renderTaskCard 里的 continue 删掉（空值也写一行）→ 与 want 不等，本用例变红。
func TestRenderTaskCardOmitsMissingRows(t *testing.T) {
	out := renderTaskCard(taskCard{Title: "t", Video: "1080P 高清"})
	want := taskCardHeader + "\n" +
		taskCardIndent + "标题" + strings.Repeat(" ", taskCardLabelWidth-download.DisplayWidth("标题")+taskCardGap) + "t\n" +
		taskCardIndent + "视频流" + strings.Repeat(" ", taskCardLabelWidth-download.DisplayWidth("视频流")+taskCardGap) + "1080P 高清\n"
	if out != want {
		t.Errorf("缺失字段没有整行省略：\n got %q\nwant %q", out, want)
	}
	for _, absent := range []string{"BV", "UP", "分P", "音频流", "输出路径"} {
		if strings.Contains(out, absent) {
			t.Errorf("字段缺失却打出了 %q 行：%q", absent, out)
		}
	}
}

// 卡片是直接写终端的（Logger.Printf 不做单行清洗），而标题/UP 名都是服务端可控文本：
// 里面的换行与 ANSI 序列不能伪造出卡片行（上游 RF-54/RF-70 的同类加固）。
func TestRenderTaskCardSanitizesServerControlledValues(t *testing.T) {
	out := renderTaskCard(taskCard{Title: "标题\n伪造行\x1b[91m", Owner: "UP\r名"})
	if got := strings.Count(out, "\n"); got != 3 {
		t.Errorf("换行注入没有被清洗：换行数 %d，实际 %q", got, out)
	}
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("控制字符没有被清洗：%q", out)
	}
}

// 卡片直接写控制台，必须走 ConsoleLock/进度行收尾那条既有约定：
// 当前行上停着进度条时先换行再打卡片，否则卡片第一行会与进度残影挤在同一行。
func TestPrintTaskCardFinishesProgressLineFirst(t *testing.T) {
	util.SetProgressLineActive(true)
	t.Cleanup(func() { util.SetProgressLineActive(false) })
	out := captureStdout(t, func() { printTaskCard(taskCard{Title: "t"}) })
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("进度行还留在当前行时，卡片应先换行再打：%q", out)
	}
	// 卡片走内容通道：无时间戳、以标题行开头（改前是 28 列缩进的日志行）。
	if !strings.HasPrefix(out, "\n"+taskCardHeader+"\n") {
		t.Errorf("卡片应走内容通道（无时间戳、以标题行开头）：%q", out)
	}
	if regexp.MustCompile(`\[\d{2}:\d{2}:\d{2}\]`).MatchString(out) {
		t.Errorf("卡片不该带日志时间戳：%q", out)
	}
}

// ---- 接线：真实下载路径打，解析/机读模式不打 ----

// 变异验证：
//   - 删掉 downloadOnePage 里的 printTaskCard 调用 → 第 1 段「真实下载路径」断言变红；
//   - 把它挪到 PrintURLs/OnlyShowInfo/InfoJSON 的提前 return 之前 → 对应那段的断言变红。
func TestTaskCardWiring(t *testing.T) {
	t.Chdir(t.TempDir())

	cdn, noRewrite := newFakeCDN(t)
	api := newFakeAPI(t, cdn.URL)

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(api.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"
	p := parser.NewParser(client, pcfg)

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 10, OwnerName: "某某UP"}
	vInfo := &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}}
	pages := []entity.Page{page}

	run := func(cfg config.MyOption) string {
		cfg.ForceReplaceHost, cfg.AllowPcdn = noRewrite()
		cfg.SkipCover = true
		cfg.SkipSubtitle = true
		cfg.RetryDelay = 1 // 页面若失败会走满重试，把退避压到 1ms 让用例快
		return captureStdout(t, func() {
			wfRun(cfg, client, p, page, vInfo, pages, download.DownloadConfig{Client: client, RetryCount: 1, RetryDelayMs: 1})
		})
	}

	// 1) 真实下载路径：卡片出现在「已选择的流」之后、开始下载之前。
	cfgDL := config.DefaultMyOption()
	cfgDL.SkipMux = true // 不需要 ffmpeg
	out := run(cfgDL)
	if !strings.Contains(out, taskCardHeader) {
		t.Fatalf("真实下载路径没有任务卡：%q", out)
	}
	for _, want := range []string{"标题", "BV", page.Bvid(), "UP", "某某UP", "视频流", "音频流", "输出路径", "out.m4a.mp4"} {
		if !strings.Contains(out, want) {
			t.Errorf("任务卡缺少 %q：%q", want, out)
		}
	}
	selected := strings.Index(out, "已选择的视频流")
	card := strings.Index(out, taskCardHeader)
	started := strings.Index(out, "开始下载P1视频")
	if selected < 0 || card < 0 || started < 0 {
		t.Fatalf("用例没走完预期路径（已选择的流=%d 任务卡=%d 开始下载=%d）：%q", selected, card, started, out)
	}
	if card < selected {
		t.Errorf("任务卡应打在「已选择的流」之后：卡片 %d < 已选择 %d", card, selected)
	}
	if started < card {
		t.Errorf("任务卡应打在开始下载之前：开始下载 %d < 卡片 %d", started, card)
	}

	// 2) --hide-streams 的契约是「不打印流清单」：卡片里两条流也省略，其余行照打。
	cfgHide := config.DefaultMyOption()
	cfgHide.SkipMux = true
	cfgHide.HideStreams = true
	outHide := run(cfgHide)
	if !strings.Contains(outHide, taskCardHeader) {
		t.Errorf("--hide-streams 不该把整张卡片也去掉：%q", outHide)
	}
	// 断言只覆盖卡片本身：--hide-streams 是「不要显示所有**可用**流」（见 --help），
	// 「已选择的视频流/音频流」两段不在这个契约里（它们是你这次真正要下的东西）。
	cardStart := strings.Index(outHide, taskCardHeader)
	if cardStart < 0 {
		t.Fatalf("--hide-streams 下应仍有卡片：%q", outHide)
	}
	if card := outHide[cardStart:]; strings.Contains(card, "视频流") || strings.Contains(card, "音频流") {
		t.Errorf("--hide-streams 下卡片仍打了流信息：%q", card)
	}

	// 3) 三种「只解析/只输出数据」的模式各有输出契约，都不能混进任务卡。
	cfgInfo := config.DefaultMyOption()
	cfgInfo.OnlyShowInfo = true
	outInfo := run(cfgInfo)
	if strings.Contains(outInfo, taskCardHeader) {
		t.Errorf("-I 不该打任务卡：%q", outInfo)
	}
	if !strings.Contains(outInfo, "可用流（1）") {
		t.Errorf("-I 的流清单没出现（用例没走到那条路径）：%q", outInfo)
	}

	cfgURLs := config.DefaultMyOption()
	cfgURLs.PrintURLs = true
	outURLs := run(cfgURLs)
	if strings.Contains(outURLs, taskCardHeader) {
		t.Errorf("--print-urls 不该打任务卡：%q", outURLs)
	}
	if !strings.Contains(outURLs, cdn.URL+"/v.m4s") {
		t.Errorf("--print-urls 没有输出直链：%q", outURLs)
	}

	cfgJSON := config.DefaultMyOption()
	cfgJSON.InfoJSON = true
	outJSON := run(cfgJSON)
	if strings.Contains(outJSON, taskCardHeader) {
		t.Errorf("--info-json 不该打任务卡：%q", outJSON)
	}
	if !strings.Contains(outJSON, "\"video\"") {
		t.Errorf("--info-json 没有输出元数据：%q", outJSON)
	}
}
