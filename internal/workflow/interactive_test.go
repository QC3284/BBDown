package workflow

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

// -i 的渐进式选择（v3 ①）：档位菜单 → 输入序号 → 只显示该档的流表 → 选流 → 确认行。
//
// 全部离线：假 API 回一份多档 playurl，假 CDN 回字节；stdin 用包级 stdinReader 注入。
// 「零额外 fetch」用假 API 上的请求计数断言——档位选择只做内存过滤，不重新解析。

// tierLayout 决定假 playurl 里有哪些档位（以及顺序）——用例按「风险流在哪一档」挑布局。
type tierLayout int

const (
	// layoutPlain：8K + 1080P 高码率×2 + 720P 高清（没有风险流）。
	layoutPlain tierLayout = iota
	// layoutDoviFirst：杜比视界 + 1080P 高码率×2 + 720P——风险流是最高档，★ 的位移看得出来。
	layoutDoviFirst
	// layoutDoviAfter8K：8K + 杜比视界 + 1080P 高码率×2 + 720P——风险流既不在全局 0 号、
	// 也不在第一档，用来钉住 ⚠ 的编号空间（现有夹具的风险流恰好在 0 号，看不出这个问题）。
	layoutDoviAfter8K
)

func doviTrackJSON(cdnURL string) string {
	return fmt.Sprintf("{'id':126,'codecid':12,'codecs':'hev1.2.4','bandwidth':30000,'width':3840,'height':2160,'frame_rate':'60','base_url':'%s/v.dovi.m4s'}", cdnURL)
}

func eightKTrackJSON(cdnURL string) string {
	return fmt.Sprintf("{'id':127,'codecid':12,'codecs':'hevc','bandwidth':20000,'width':7680,'height':4320,'frame_rate':'60','base_url':'%s/v8k.m4s'}", cdnURL)
}

// multiTierPlayurl 按布局造一份 playurl（视频档位 + 一条音频）。
func multiTierPlayurl(cdnURL string, layout tierLayout) string {
	videos := []string{}
	switch layout {
	case layoutDoviFirst:
		videos = append(videos, doviTrackJSON(cdnURL))
	case layoutDoviAfter8K:
		videos = append(videos, eightKTrackJSON(cdnURL), doviTrackJSON(cdnURL))
	default:
		videos = append(videos, eightKTrackJSON(cdnURL))
	}
	videos = append(videos,
		fmt.Sprintf("{'id':112,'codecid':12,'codecs':'hevc','bandwidth':5000,'width':1920,'height':1080,'frame_rate':'60','base_url':'%s/v1080h.m4s'}", cdnURL),
		fmt.Sprintf("{'id':112,'codecid':7,'codecs':'avc1.640032','bandwidth':3000,'width':1920,'height':1080,'frame_rate':'60','base_url':'%s/v1080a.m4s'}", cdnURL),
		fmt.Sprintf("{'id':64,'codecid':7,'codecs':'avc1.640028','bandwidth':1500,'width':1280,'height':720,'frame_rate':'30','base_url':'%s/v720.m4s'}", cdnURL),
	)
	tmpl := "{'code':0,'data':{'dash':{'duration':100,'video':[" + strings.Join(videos, ",") +
		"],'audio':[{'id':30280,'codecid':0,'codecs':'mp4a.40.2','bandwidth':132,'base_url':'" + cdnURL + "/a.m4s'}]}}}"
	return strings.ReplaceAll(tmpl, "'", string('"'))
}

// newMultiTierAPI 起一个假 API 并把请求数记进 hits（用来断言 -i 不发额外请求）。
func newMultiTierAPI(t *testing.T, cdnURL string, layout tierLayout, hits *int32) *httptest.Server {
	t.Helper()
	body := multiTierPlayurl(cdnURL, layout)
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.Close)
	return api
}

// tierFlowFixture 是一次 -i / 非 -i 运行需要的全部脚手架。
type tierFlowFixture struct {
	client *util.HTTPClient
	p      *parser.Parser
	page   entity.Page
	vInfo  *entity.VInfo
	pages  []entity.Page
	cfg    config.MyOption
	hits   *int32
	cdnURL string
}

func newTierFlowFixture(t *testing.T, layout tierLayout) *tierFlowFixture {
	t.Helper()
	t.Chdir(t.TempDir())

	cdn, noRewrite := newFakeCDN(t)
	var hits int32
	api := newMultiTierAPI(t, cdn.URL, layout, &hits)

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	pcfg := config.DefaultAppSettings()
	pcfg.Host = strings.TrimPrefix(api.URL, "https://")
	pcfg.TvHost = pcfg.Host
	pcfg.EpHost = pcfg.Host
	pcfg.Wbi = "test_wbi_key"

	page := entity.Page{Index: 1, Aid: "1", Cid: "2", Title: "t", Dur: 100, OwnerName: "某UP"}
	cfg := config.DefaultMyOption()
	cfg.SkipMux = true // 不需要 ffmpeg：保留原始轨道即可
	cfg.SkipCover = true
	cfg.SkipSubtitle = true
	cfg.ForceReplaceHost, cfg.AllowPcdn = noRewrite()
	cfg.RetryDelay = 1
	return &tierFlowFixture{
		client: client,
		p:      parser.NewParser(client, pcfg),
		page:   page,
		vInfo:  &entity.VInfo{Title: "t", PagesInfo: []entity.Page{page}},
		pages:  []entity.Page{page},
		cfg:    cfg,
		hits:   &hits,
		cdnURL: cdn.URL,
	}
}

// run 跑一次 downloadOnePage，返回（捕获的终端输出, 本次新增的 API 请求数）。
func (f *tierFlowFixture) run(t *testing.T, cfg config.MyOption) (string, int32) {
	t.Helper()
	before := atomic.LoadInt32(f.hits)
	var out string
	out = captureStdout(t, func() {
		wfRun(cfg, f.client, f.p, f.page, f.vInfo, f.pages,
			download.DownloadConfig{Client: f.client, RetryCount: 1, RetryDelayMs: 1})
	})
	return out, atomic.LoadInt32(f.hits) - before
}

// setStdin 注入交互输入并在用例结束时还原（stdin 接缝见 workflow.go 的 stdinReaderValue/
// setStdinReader：接缝是原子指针，因为 readIntSafe 的读在另一个协程里，裸变量会被 -race 报竞争）。
func setStdin(t *testing.T, input string) {
	t.Helper()
	old := stdinReaderValue()
	setStdinReader(strings.NewReader(input))
	t.Cleanup(func() { setStdinReader(old) })
}

// TestInteractiveTierFlowIsLayeredAndFetchesNothing：-i 的全流程（v3 ①）——
// 档位菜单（★ 推荐档）→ 输入序号 → **只显示该档**的流表 → 选流 → 确认行含预计大小与输出路径；
// 并且整条档位选择路径零额外 fetch（与同夹具的非交互运行请求数相同）。
func TestInteractiveTierFlowIsLayeredAndFetchesNothing(t *testing.T) {
	f := newTierFlowFixture(t, layoutPlain)

	// 基线：非交互（自动选档）跑一次，拿请求数。
	_, plainHits := f.run(t, f.cfg)

	setStdin(t, "2\n0\n0\n") // 档位 2（720P 高清）→ 档内视频流 0 → 音频流 0
	intCfg := f.cfg
	intCfg.Interactive = true
	out, intHits := f.run(t, intCfg)

	// ① 档位菜单：三档、一行两档、★ 在推荐档（未开 --compat = 最高档 8K）后面。
	if !strings.Contains(out, "该稿件共 3 档清晰度:") {
		t.Fatalf("没有档位菜单：%q", out)
	}
	if !strings.Contains(out, "0. 8K 超高清 ★") {
		t.Errorf("★ 应当标在推荐档（最高档）上：%q", out)
	}
	if !strings.Contains(out, "1. 1080P 高码率") || !strings.Contains(out, "2. 720P 高清") {
		t.Errorf("菜单没有列全档位：%q", out)
	}

	// ② 选中档之后**只显示该档**的流表：提示之后不该再出现其它档的名字。
	promptAt := strings.Index(out, "请选择最想要的清晰度")
	if promptAt < 0 {
		t.Fatalf("没有档位输入提示：%q", out)
	}
	after := out[promptAt:]
	if !strings.Contains(after, "720P 高清") {
		t.Errorf("选中档的流表里没有该档：%q", after)
	}
	if strings.Contains(after, "8K 超高清") || strings.Contains(after, "1080P 高码率") {
		t.Errorf("只应显示选中档的流表，其它档不该出现：%q", after)
	}

	// ③ 档内序号映射回全局下标：选了 720P，选流那两行就该是 720P。
	if !strings.Contains(out, "[视频] [720P 高清]") {
		t.Errorf("档内序号没有映射回该档的流：%q", out)
	}
	// ④ 确认行：预计总大小 + 输出路径（v3 ⑥）。
	if !strings.Contains(out, "预计总大小 ≈ ") || !strings.Contains(out, "输出: ") {
		t.Errorf("确认行缺少预计大小/输出路径：%q", out)
	}

	// ⑤ 零额外 fetch：档位选择只做内存过滤，请求数与自动选档那次相同。
	if intHits != plainHits {
		t.Errorf("-i 的档位选择必须零额外 fetch：自动选档 %d 次 API 请求，-i %d 次", plainHits, intHits)
	}
	if plainHits == 0 {
		t.Fatal("夹具没发出任何 API 请求：用例没走到解析路径")
	}
}

// TestDefaultPathHasNoTierMenuOrPrompts：不带 -i 时不应出现档位菜单与任何交互提示，
// 自动选档行为不变（仍取排序后的第一档）。
func TestDefaultPathHasNoTierMenuOrPrompts(t *testing.T) {
	f := newTierFlowFixture(t, layoutPlain)
	out, _ := f.run(t, f.cfg)

	for _, gone := range []string{"该稿件共", "请选择最想要的清晰度", "请选择一条视频流", "请选择一条音频流"} {
		if strings.Contains(out, gone) {
			t.Errorf("非交互模式不该出现 %q：%q", gone, out)
		}
	}
	if !strings.Contains(out, "[视频] [8K 超高清]") {
		t.Errorf("自动选档应当取最高档：%q", out)
	}
	// v3 的信息行在非交互模式下同样要有（⑦ 的默认顺序）。
	if !strings.Contains(out, "UP主: 某UP") || !strings.Contains(out, "预计总大小 ≈ ") {
		t.Errorf("非交互模式缺少 v3 信息行：%q", out)
	}
}

// TestSidecarLineWiredIntoDownloadPath：侧车状态行真的从下载路径打出来（t16 修订 ⑦）——
// 封面下成功后在媒体下载前打成「侧车: 封面 ✔」；没启用的字幕/弹幕不出现。
func TestSidecarLineWiredIntoDownloadPath(t *testing.T) {
	f := newTierFlowFixture(t, layoutPlain)
	cfg := f.cfg
	cfg.SkipCover = false
	f.vInfo.Pic = f.cdnURL + "/cover.jpg" // 假 CDN 回字节，封面能下成功

	out, _ := f.run(t, cfg)
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "侧车:") {
			line = l
		}
	}
	if !strings.Contains(line, "侧车: 封面 ✔") {
		t.Fatalf("下载路径没有打出侧车状态行：%q（输出 %q）", line, out)
	}
	if strings.Contains(line, "字幕") || strings.Contains(line, "弹幕") {
		t.Errorf("未启用的侧车不该出现在状态行里：%q", line)
	}
}

// TestTierMenuAndWarningUnderCompat：--compat 下 ★ 跳到避开杜比视界/ HDR 后的第一档，
// 且流表后的 ⚠ 标记点名有风险的流（普通稿件不出现 ⚠）。
func TestTierMenuAndWarningUnderCompat(t *testing.T) {
	f := newTierFlowFixture(t, layoutDoviFirst) // fixture 的最高档是杜比视界(126)

	// 非 --compat：★ 在最高档（杜比视界那一档）；⚠ 的编号是**屏幕上那张表**的编号——
	// 这里选中的就是那一档（表里只有这一行）→ 「0 号流」，且该档内没有可换的流，
	// 所以给的是「加 --compat」而不是「换 N 号」（t19 修订前这里按全表编号写死「可换 1 号」）。
	setStdin(t, "0\n0\n0\n")
	plain := f.cfg
	plain.Interactive = true
	outNoCompat, _ := f.run(t, plain)
	if !strings.Contains(outNoCompat, "0. 杜比视界 ★") {
		t.Errorf("未开 --compat 时推荐档应是最高档（杜比视界）：%q", outNoCompat)
	}
	if !strings.Contains(outNoCompat, "⚠ 0 号流为杜比视界：需支持 DOVI 的播放器；加 --compat 可自动避开") {
		t.Errorf("缺少杜比视界 ⚠ 标记：%q", outNoCompat)
	}
	if strings.Contains(outNoCompat, "可换") {
		t.Errorf("该档里没有可换的流，不该给出换序号建议：%q", outNoCompat)
	}
	// 选中档的流表只列该档：那一档只有一条流，屏幕上就该只有一行。
	promptAt := strings.Index(outNoCompat, "请选择最想要的清晰度")
	if after := outNoCompat[promptAt:]; strings.Contains(after, "1080P 高码率") || strings.Contains(after, "720P 高清") {
		t.Errorf("选中档后不该列出其它档：%q", after)
	}
	if rows := displayedVideoRows(t, outNoCompat); rows != 1 {
		t.Errorf("杜比视界档只有一条流，屏幕上应当只有 1 行，实际 %d：%q", rows, outNoCompat)
	}

	// --compat：有风险的档位在显示前就被 filterCompatTracks 剔掉，菜单里不再有杜比视界，
	// ★ 落到剩下的最高档（1080P 高码率）。
	setStdin(t, "0\n0\n0\n")
	compat := f.cfg
	compat.Interactive = true
	compat.Compat = true
	outCompat, _ := f.run(t, compat)
	menuStart := strings.Index(outCompat, "该稿件共")
	menuEnd := strings.Index(outCompat, "请选择最想要的清晰度")
	if menuStart < 0 || menuEnd < menuStart {
		t.Fatalf("没有档位菜单：%q", outCompat)
	}
	// 只看菜单那一段：--compat 的既有提示行本身就会提到「杜比视界」，别误伤。
	if menu := outCompat[menuStart:menuEnd]; strings.Contains(menu, "杜比视界") {
		t.Errorf("--compat 下菜单里不该还有杜比视界档：%q", menu)
	}
	if !strings.Contains(outCompat, "0. 1080P 高码率 ★") {
		t.Errorf("--compat 下 ★ 应该落到剩下的最高档：%q", outCompat)
	}

	// 普通稿件：没有 ⚠。
	plainFixture := newTierFlowFixture(t, layoutPlain)
	normalOut, _ := plainFixture.run(t, plainFixture.cfg)
	if strings.Contains(normalOut, "⚠") {
		t.Errorf("普通流不该出现 ⚠ 标记：%q", normalOut)
	}
}

// TestWarnIndicesMatchTheDisplayedTable：⚠ 里的「N 号流」「可换 M 号」必须落在**屏幕上那张表**
// 的行号范围内（v3 ⑤ 的编号空间 = 刚打印的流表，不是整个 ParsedResult）。
//
// 夹具用 layoutDoviAfter8K：风险流（杜比视界）在全局 1 号、第二档——其余夹具的风险流恰好在
// 全局 0 号/第一档，编号空间写错了也看不出来（t19 的来由）。
//
// 变异验证：把 workflow.go 里的 warnTracks 改回 result.VideoTracks（编号空间退回全表）→
// 第 ② 段（屏幕上没有风险流却报 ⚠）与第 ③ 段（说 1 号而不是 0 号）都会红。
func TestWarnIndicesMatchTheDisplayedTable(t *testing.T) {
	// ① 非交互：全表都在屏幕上，编号自然就是全局的（1 号风险流、建议换 0 号）。
	f := newTierFlowFixture(t, layoutDoviAfter8K)
	full, _ := f.run(t, f.cfg)
	fullWarn := warnLineOf(t, full)
	if !strings.Contains(fullWarn, "⚠ 1 号流为杜比视界：需支持 DOVI 的播放器；要通用兼容可换 0 号或 --compat") {
		t.Fatalf("全表下的 ⚠ 行 = %q（输出 %q）", fullWarn, full)
	}
	assertWarnNumbersWithinRows(t, full, warnNumbers(t, fullWarn))

	// ② -i 选**安全档**（1080P 高码率 = 第 2 档）：屏幕上没有风险流，就不该有 ⚠
	//    （按全表编号的实现会指着一行用户根本看不到的流提示）。
	setStdin(t, "2\n0\n0\n")
	intCfg := f.cfg
	intCfg.Interactive = true
	outSafe, _ := f.run(t, intCfg)
	if got := warnLineOf(t, outSafe); got != "" {
		t.Errorf("屏幕上没有风险流时不该有 ⚠：%q", got)
	}

	// ③ -i 选**风险档**（杜比视界 = 第 1 档）：那张表只有一行，⚠ 就得说 0 号。
	setStdin(t, "1\n0\n0\n")
	outRisky, _ := f.run(t, intCfg)
	riskyWarn := warnLineOf(t, outRisky)
	if !strings.Contains(riskyWarn, "⚠ 0 号流为杜比视界") {
		t.Fatalf("分层显示下的 ⚠ 用了全表编号：%q（输出 %q）", riskyWarn, outRisky)
	}
	if strings.Contains(riskyWarn, "可换") {
		t.Errorf("该档里没有可换的流，不该给出换序号建议：%q", riskyWarn)
	}
	assertWarnNumbersWithinRows(t, outRisky, warnNumbers(t, riskyWarn))
}

// warnLineOf 取出输出里的 ⚠ 行（没有就返回空串；多条直接判错——⚠ 一次只该有一条）。
func warnLineOf(t *testing.T, out string) string {
	t.Helper()
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "⚠") {
			continue
		}
		if line != "" {
			t.Fatalf("期望只有一条 ⚠ 行，实际多条：%q", out)
		}
		line = l
	}
	return line
}

// warnNumbers 解出 ⚠ 行里的流号（「N 号流」与「可换 M 号」里的数字）。
func warnNumbers(t *testing.T, line string) []int {
	t.Helper()
	var nums []int
	for _, m := range regexp.MustCompile("[0-9]+ 号").FindAllString(line, -1) {
		n, err := strconv.Atoi(strings.TrimSuffix(m, " 号"))
		if err != nil {
			t.Fatalf("⚠ 行里的序号解不出来：%q（%v）", m, err)
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		t.Fatalf("⚠ 行里没有任何序号：%q", line)
	}
	return nums
}

// assertWarnNumbersWithinRows 断言 ⚠ 里的每个序号都落在**刚打印的那张流表**的行号范围内。
func assertWarnNumbersWithinRows(t *testing.T, out string, nums []int) {
	t.Helper()
	rows := displayedVideoRows(t, out)
	if rows == 0 {
		t.Fatalf("没在输出里找到流表的行：%q", out)
	}
	for _, n := range nums {
		if n < 0 || n >= rows {
			t.Errorf("⚠ 里的 %d 号不在屏幕上那张表的行号范围 [0,%d) 内：%q", n, rows, out)
		}
	}
}

// displayedVideoRows 数出最后那张视频流表有几行（数据行形如「0. [dfn] …」）。
// 段落取「共计N条视频流」到音频段之间，避免把音频行与「已选择的流」也数进来。
func displayedVideoRows(t *testing.T, out string) int {
	t.Helper()
	// 锚在表头行「共计N条视频流.」上（带句点）：不带句点会先撞上选流提示
	// 「请选择一条视频流(输入序号)」——那句在表之后，LastIndex 会把整张表切掉。
	vi := strings.LastIndex(out, "条视频流.")
	if vi < 0 {
		return 0
	}
	rest := out[vi:]
	if ai := strings.Index(rest, "条音频流."); ai >= 0 {
		rest = rest[:ai]
	}
	rows := 0
	for _, l := range strings.Split(rest, "\n") {
		if strings.Contains(l, ". [") {
			rows++
		}
	}
	return rows
}
