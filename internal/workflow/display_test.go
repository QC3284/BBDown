package workflow

import (
	"io"
	"os"
	"strings"
	"testing"

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

// TestPrintVideoHeaderMatchesUpstream 钉住稿件头部四行的标签与格式
// （上游 Workflow.cs:142-153：视频标题 / 发布时间(带时区) / 视频URL / UP主页）。
// 此前标题与 URL 都是裸值、UP 主页干脆不打印，用户分不清每行是什么。
func TestPrintVideoHeaderMatchesUpstream(t *testing.T) {
	vInfo := &entity.VInfo{
		Title:   "示例稿件",
		PubTime: 1541500000,
		PagesInfo: []entity.Page{{
			Index: 1, Aid: "170001", Cid: "2",
			OwnerMid: "12345",
		}},
	}

	out := captureStdout(t, func() { printVideoHeader(vInfo, false) })
	for _, want := range []string{"视频标题: 示例稿件", "发布时间: ", "视频URL: https://www.bilibili.com/video/", "UP主页: https://space.bilibili.com/12345"} {
		if !strings.Contains(out, want) {
			t.Errorf("头部缺少 %q，实际输出 %q", want, out)
		}
	}
	// 上游的时间戳带本地时区偏移（zzz），不带偏移的分支格式曾被用在这里。
	if !strings.Contains(out, "+08:00") && !strings.Contains(out, "-0") && !strings.Contains(out, "+0") {
		t.Errorf("发布时间应带时区偏移，实际输出 %q", out)
	}

	// 国际版不打印 bilibili.com 的 URL（上游 !myOption.UseIntlApi 条件）。
	out = captureStdout(t, func() { printVideoHeader(vInfo, true) })
	if strings.Contains(out, "视频URL: ") {
		t.Errorf("国际版不应打印视频URL，实际输出 %q", out)
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
