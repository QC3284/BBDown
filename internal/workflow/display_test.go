package workflow

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown-Go/internal/config"
	"github.com/QC3284/BBDown-Go/internal/entity"
	"github.com/QC3284/BBDown-Go/internal/util"
)

// captureStdout 把 os.Stdout 换成管道，收集 fn 期间的全部终端输出。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestPrintVideoHeaderMatchesV3 钉住 v3 定稿的稿件头部：只剩「视频标题: …」（青色）一行。
//
// 上游 Workflow.cs:142-153 打四行（视频标题 / 发布时间(带时区) / 视频URL / UP主页）；v3
// （BBDownT 口径，docs/cli-concept-bbdt-v3.png）把它们收成「标题一行 + 每个分P 一条元信息行」：
// 发布时间、UP、BV/av、时长、分P 数由 renderMetaLine 承载（逐字断言见 infolines_test.go）。
// 这是**有意偏离上游 v1.6.20** 的观感变更——同一屏里不该两处报同一件事。
//
// 旧断言（四行都在头部）按新口径反转：每一段信息的意图都保留，只是换了承载者——
// 发布时间 → 元信息行的「发布 YYYY-MM-DD」；UP 主页 → 元信息行的「UP主: 名」；
// 视频 URL → 元信息行的「BV… (avN)」（站点 URL 那行在 v3 里取消，故国际版也不再有差异）。
func TestPrintVideoHeaderMatchesV3(t *testing.T) {
	page := entity.Page{
		Index: 1, Aid: "170001", Cid: "2",
		OwnerMid: "12345", OwnerName: "某UP", Dur: 2055,
	}
	vInfo := &entity.VInfo{
		Title:     "示例稿件",
		PubTime:   1541500000,
		PagesInfo: []entity.Page{page},
	}

	out := captureStdout(t, func() { printVideoHeader(vInfo, false) })
	if !strings.Contains(out, "视频标题: 示例稿件") {
		t.Errorf("头部缺少标题行，实际输出 %q", out)
	}
	if !strings.Contains(out, util.AnsiCyan) {
		t.Errorf("标题行应当是青色（v3 定稿的标题行），实际输出 %q", out)
	}
	for _, gone := range []string{"发布时间: ", "视频URL: ", "UP主页: "} {
		if strings.Contains(out, gone) {
			t.Errorf("v3 头部不该再有 %q（信息改由元信息行承载，避免同一屏报两遍），实际输出 %q", gone, out)
		}
	}
	// v3 头部与解析模式无关：旧「国际版不打 URL」的差异随 URL 行一起消失。
	// 不比较整段输出——日志行带时间戳，两次调用跨过毫秒边界就会误报（踩过一次）。
	intl := captureStdout(t, func() { printVideoHeader(vInfo, true) })
	if !strings.Contains(intl, "视频标题: 示例稿件") {
		t.Errorf("国际版头部缺少标题行：%q", intl)
	}
	for _, gone := range []string{"发布时间: ", "视频URL: ", "UP主页: "} {
		if strings.Contains(intl, gone) {
			t.Errorf("国际版头部不该有 %q（v3 头部只剩标题行）：%q", gone, intl)
		}
	}

	// 标题行丢掉的信息必须真的还在：元信息行逐条承载它们。
	meta := renderMetaLine(pageInfoOf(page, vInfo, 1))
	wantDate := "发布 " + time.Unix(vInfo.PubTime, 0).Format("2006-01-02")
	for _, want := range []string{"UP主: 某UP", page.Bvid(), "(av170001)", wantDate, "34:15", "分P 1"} {
		if !strings.Contains(meta, want) {
			t.Errorf("元信息行缺少 %q，实际 %q", want, meta)
		}
	}
}

// TestApplySteinGateFallback 对齐上游 Workflow.cs:156-160：互动视频不支持 TV 端下载，
// 打了 -t 要就地改回默认解析并给出提示，否则 TV 接口拿不到分P。
func TestApplySteinGateFallback(t *testing.T) {
	cfg := config.DefaultMyOption()
	cfg.UseTvAPI = true
	vInfo := &entity.VInfo{IsSteinGate: true}
	out := captureStdout(t, func() { applySteinGateFallback(&cfg, vInfo) })
	if cfg.UseTvAPI {
		t.Error("互动视频 + -t 必须改回默认解析")
	}
	if !strings.Contains(out, "视频为互动视频") {
		t.Errorf("缺少提示，实际输出 %q", out)
	}

	// 非互动视频不动用户的选择
	cfg2 := config.DefaultMyOption()
	cfg2.UseTvAPI = true
	captureStdout(t, func() { applySteinGateFallback(&cfg2, &entity.VInfo{}) })
	if !cfg2.UseTvAPI {
		t.Error("普通视频不应改变 -t 的选择")
	}
}

// TestGetSelectedPagesAutoSelectNotice 对齐上游 Pages.cs:22-33：按 epid 或 URL 里的 p 参数
// 自动选集数时必须告诉用户，否则用户看到「已选择: 3」不知道是谁选的；
// p 参数要按查询串解析，不能只认 "?p=" 开头（?a=1&p=4 同样有效）。
func TestGetSelectedPagesAutoSelectNotice(t *testing.T) {
	cfg := config.DefaultMyOption()

	var got []string
	var err error
	out := captureStdout(t, func() {
		got, err = getSelectedPages(&cfg, &entity.VInfo{Index: "3"}, "https://www.bilibili.com/video/BV1xx")
	})
	if err != nil {
		t.Fatalf("getSelectedPages: %v", err)
	}
	if strings.Join(got, ",") != "3" {
		t.Errorf("按 epid 自动选集数 = %v，期望 [3]", got)
	}
	if !strings.Contains(out, "程序已自动选择你输入的集数") {
		t.Errorf("缺少自动选择提示，实际输出 %q", out)
	}

	out = captureStdout(t, func() {
		got, err = getSelectedPages(&cfg, &entity.VInfo{}, "https://www.bilibili.com/video/BV1xx?a=1&p=4")
	})
	if err != nil {
		t.Fatalf("getSelectedPages: %v", err)
	}
	if strings.Join(got, ",") != "4" {
		t.Errorf("按 ?a=1&p=4 自动选集数 = %v，期望 [4]", got)
	}
	if !strings.Contains(out, "程序已自动选择你输入的集数") {
		t.Errorf("缺少自动选择提示，实际输出 %q", out)
	}

	// 既没有 epid 也没有 p 参数：下载全部分P，不该出现提示。
	out = captureStdout(t, func() {
		got, err = getSelectedPages(&cfg, &entity.VInfo{}, "https://www.bilibili.com/video/BV1xx")
	})
	if err != nil || got != nil {
		t.Fatalf("无选集信息时应返回 nil(ALL)，实际 %v err=%v", got, err)
	}
	if strings.Contains(out, "程序已自动选择你输入的集数") {
		t.Errorf("没有自动选集却打了提示：%q", out)
	}
}
