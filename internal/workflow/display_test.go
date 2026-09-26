package workflow

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
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

// TestPrintVideoHeaderMatchesTargetForm 钉住稿件头的形态：标题行 + 一行信息
// （UP / 分P / 时长 / BV / 发布日期）。改前是四行带日志前缀的「视频标题: / 发布时间: /
// 视频URL: / UP主页:」，与下载日志平铺在同一层，标题看不出是标题。
//
// 变异验证：把信息行改回多行日志（每行带前缀）→ 行数与逐字节断言同时红。
func TestPrintVideoHeaderMatchesTargetForm(t *testing.T) {
	vInfo := &entity.VInfo{
		Title:   "示例稿件",
		PubTime: 1541500000,
		PagesInfo: []entity.Page{{
			Index: 1, Aid: "170001", Cid: "2", Dur: 2055,
			OwnerName: "碧诗", OwnerMid: "12345",
		}},
	}

	out := captureStdout(t, func() { printVideoHeader(vInfo, false) })
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("稿件头应当是「标题行 + 信息行」两行，实际 %d 行：%q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "── 示例稿件 ") || !strings.HasSuffix(lines[0], "─") {
		t.Errorf("首行应是标题行 ── 示例稿件 ────：%q", lines[0])
	}
	// 日期用本地时区的 YYYY-MM-DD（用例与实现取同一个时区，跨时区 CI 不会假红）。
	wantInfo := " UP 碧诗 · P1/1 · 34:15 · BV17x411w7KC · " + time.Unix(vInfo.PubTime, 0).Format("2006-01-02")
	if lines[1] != wantInfo {
		t.Errorf("信息行不符：\n得到 %q\n期望 %q", lines[1], wantInfo)
	}
	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2}$`).MatchString(lines[1]) {
		t.Errorf("发布日期应是 YYYY-MM-DD 结尾：%q", lines[1])
	}
	// 内容行不带时间戳前缀（改前每条都带 28 字符的 [日期 时分秒.毫秒] - ）。
	if strings.Contains(out, "] ") {
		t.Errorf("稿件头不该带日志时间戳前缀：%q", out)
	}

	// 国际版不打印 bilibili.com 的 BV（上游 !myOption.UseIntlApi 条件）。
	out = captureStdout(t, func() { printVideoHeader(vInfo, true) })
	if strings.Contains(out, "BV17x411w7KC") {
		t.Errorf("国际版不应打印 BV，实际输出 %q", out)
	}
}

// TestVideoInfoRowOmitsEmptyFields 信息行里的空字段整段省略：没有 UP 名时退回 mid、
// 没有发布日期就不留尾部分隔符（改前的方括号兜底正是「空字段打占位」的形态）。
func TestVideoInfoRowOmitsEmptyFields(t *testing.T) {
	vInfo := &entity.VInfo{
		PagesInfo: []entity.Page{{Index: 2, Aid: "170001", Cid: "2", Dur: 65, OwnerMid: "12345", OwnerName: ""}},
	}
	out := captureStdout(t, func() { printVideoHeader(vInfo, false) })
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("应有两行（标题行 + 信息行），实际 %q", out)
	}
	if want := " UP 12345 · P2/1 · 1:05 · BV17x411w7KC"; lines[1] != want {
		t.Errorf("缺少 UP 名时应退回 mid、无发布日期时不留尾部：\n得到 %q\n期望 %q", lines[1], want)
	}
	if strings.Contains(out, "[]") || strings.Contains(out, "  ·") || strings.HasSuffix(strings.TrimSpace(lines[1]), "·") {
		t.Errorf("空字段不该留下分隔符或空括号：%q", out)
	}
}

// TestFormatPageRowOmitsEmptyFields 分P 行的字段省略规则（P1 + 标题 + 时长 + cid）：
// 改前是「P1: [62131] [] [00:34:15]」，空标题留下一对空方括号。
func TestFormatPageRowOmitsEmptyFields(t *testing.T) {
	cases := []struct {
		name string
		page entity.Page
		want string
	}{
		{"全字段", entity.Page{Index: 1, Cid: "62131", Title: "第一话", Dur: 2055}, " P1  第一话 · 00:34:15 · cid 62131"},
		{"空标题", entity.Page{Index: 2, Cid: "62132", Dur: 60}, " P2  00:01:00 · cid 62132"},
		{"只有序号", entity.Page{Index: 3}, " P3"},
	}
	for _, c := range cases {
		got := formatPageRow(c.page)
		if got != c.want {
			t.Errorf("%s：formatPageRow = %q，期望 %q", c.name, got, c.want)
		}
		if strings.Contains(got, "[]") || strings.Contains(got, "  ·") {
			t.Errorf("%s：空字段不该留下空括号/空分隔符：%q", c.name, got)
		}
	}
}

// TestPrintPageListEllipsisForLongLists 超过 6 个分P 时只列前 5 与最后 1 个（上游行为），
// 中间用一行说明代替改前的六个点——用户知道少看了多少个，也知道怎么展开。
func TestPrintPageListEllipsisForLongLists(t *testing.T) {
	var pages []entity.Page
	for i := 1; i <= 12; i++ {
		pages = append(pages, entity.Page{Index: i, Cid: fmt.Sprint(1000 + i), Title: fmt.Sprintf("第%d话", i), Dur: 60 * i})
	}
	out := captureStdout(t, func() { printPageList(pages, false) })
	if !strings.Contains(out, " P1  ") || !strings.Contains(out, " P5  ") || !strings.Contains(out, " P12  ") {
		t.Errorf("应列出前 5 个与最后 1 个：%q", out)
	}
	if strings.Contains(out, " P6  ") {
		t.Errorf("中间的 P6 不该出现：%q", out)
	}
	for _, want := range []string{"其余 6 个分P已省略", "--show-all"} {
		if !strings.Contains(out, want) {
			t.Errorf("省略说明缺少 %q：%q", want, out)
		}
	}

	// --show-all：12 个全列，没有省略行。
	out = captureStdout(t, func() { printPageList(pages, true) })
	if strings.Contains(out, "已省略") {
		t.Errorf("--show-all 不该有省略说明：%q", out)
	}
	for i := 1; i <= 12; i++ {
		if !strings.Contains(out, fmt.Sprintf(" P%d  ", i)) {
			t.Errorf("--show-all 缺少 P%d：%q", i, out)
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
