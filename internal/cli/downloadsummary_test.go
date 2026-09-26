package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/spf13/cobra"
)

// 下载结束的收尾汇总（任务 G-2）：downloadTargets 整批跑完时打一行「成功/失败任务数 + 耗时」。
// 产物文件数目前拿不到（见 batchSummary.products 的说明），所以只报前两项。

// summaryStampRe 匹配事件时间戳前缀：汇总行走内容通道，不带它。
var summaryStampRe = regexp.MustCompile("^[0-9]{2}:[0-9]{2}:[0-9]{2}  ")

// TestFormatBatchSummary 是纯函数表：输入计数/耗时 → 期望字符串（纯文本形态）。
//
// 版式与设计稿一致：\`✓ 下载完成   成功 2 · 失败 0 · 1.5s\`；有失败时标记换成 ⚠
// （颜色角色的 golden 断言见 TestBatchSummaryRolesAreGolden）。
//
// 变异验证：耗时改成不取整（直接 %v）→ 第三条表的 2.4s 变红；去掉产出文件数分支 → 第三条变红；
// 失败数为 0 时也打 ⚠ → 第一条变红。
func TestFormatBatchSummary(t *testing.T) {
	cases := []struct {
		name  string
		stats batchSummary
		want  string
	}{
		{
			"全部成功",
			batchSummary{succeeded: 2, failed: 0, elapsed: 1500 * time.Millisecond, products: productsUnknown},
			"✓ 下载完成   成功 2 · 失败 0 · 1.5s",
		},
		{
			"部分失败（耗时按 100ms 取整）",
			batchSummary{succeeded: 1, failed: 2, elapsed: 65*time.Second + 234*time.Millisecond, products: productsUnknown},
			"⚠ 下载完成   成功 1 · 失败 2 · 1m5.2s",
		},
		{
			"有产出列表时报文件数",
			batchSummary{succeeded: 1, failed: 0, elapsed: 2400 * time.Millisecond, products: 4},
			"✓ 下载完成   成功 1 · 失败 0 · 产出 4 · 2.4s",
		},
		{
			"零目标",
			batchSummary{succeeded: 0, failed: 0, elapsed: 0, products: productsUnknown},
			"✓ 下载完成   成功 0 · 失败 0 · 0s",
		},
		{
			"耗时不详（稍后再看这类调用点拿不到时长）",
			batchSummary{succeeded: 3, failed: 0, elapsed: elapsedUnknown, products: productsUnknown},
			"✓ 下载完成   成功 3 · 失败 0",
		},
	}
	for _, tc := range cases {
		if got := formatBatchSummary(tc.stats); got != tc.want {
			t.Errorf("%s：formatBatchSummary = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// TestBatchSummaryRolesAreGolden 钉住收尾汇总的角色：✓ 成功色 + 文案 BOLD + 统计 MUTED；
// 有失败时 ⚠ 与「失败 N」走警告色。
//
// 变异验证：把 ✓/文案换成正文色 → 成功色断言红；把统计改成正文色 → MUTED 断言红；
// 去掉失败时的警告色分支 → 第二条断言红。
func TestBatchSummaryRolesAreGolden(t *testing.T) {
	t.Cleanup(util.ForceColorsForTest())

	ok := batchSummaryLine("下载完成", batchSummary{succeeded: 2, failed: 0, elapsed: 1500 * time.Millisecond, products: productsUnknown})
	wantOK := "\x1b[38;5;78m✓ \x1b[0m\x1b[1m下载完成\x1b[0m\x1b[38;5;245m   成功 2 · 失败 0 · 1.5s\x1b[0m"
	if got := ok.Render(); got != wantOK {
		t.Errorf("成功汇总的角色不符：\n got %q\nwant %q", got, wantOK)
	}
	if got := ok.Plain(); got != "✓ 下载完成   成功 2 · 失败 0 · 1.5s" {
		t.Errorf("成功汇总的纯文本形态不符：%q", got)
	}

	bad := batchSummaryLine("下载完成", batchSummary{succeeded: 1, failed: 2, elapsed: 1500 * time.Millisecond, products: productsUnknown})
	rendered := bad.Render()
	if !strings.HasPrefix(rendered, "\x1b[38;5;214m⚠ \x1b[0m") {
		t.Errorf("有失败时标记应是警告色：%q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[38;5;214m失败 2\x1b[0m") {
		t.Errorf("失败计数应走警告色：%q", rendered)
	}
}

// TestDownloadTargetsPrintsBatchSummary 跑一次真实的 downloadTargets：目标无法识别（走本地
// 错误路径，不碰网络），断言收尾汇总那一行确实打了出来，且计数与本次运行一致。
//
// WorkDir 必须注入临时目录：失败目标会被登记进 .bbdown-pending.json，而 pendingPath 在
// WorkDir 为空时退回**进程工作目录**（对真实用户是设计，对测试就是往仓库里写状态文件）。
// 包级守卫见 workdir_guard_test.go 的 TestMain。
//
// 变异验证：删掉 downloadTargets 里那次 util.ContentLine(batchSummaryLine…) → 本用例变红；
// 把 cfg.WorkDir 改回默认（空 = 进程工作目录）→ 「清单落在注入目录」的断言与包级守卫一起变红。
func TestDownloadTargetsPrintsBatchSummary(t *testing.T) {
	installInterruptSpy(t) // 下载路径会取中断 ctx：换成假实现，不真注册信号

	workDir := t.TempDir() // 失败登记必须落在临时目录，不能是进程工作目录
	cfg := config.DefaultMyOption()
	cfg.WorkDir = workDir

	client := util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil)
	var runErr error
	console := captureStdout(t, func() {
		runErr = downloadTargets(context.Background(), &cobra.Command{}, cfg, client, []string{"这不是一个视频地址"})
	})
	if runErr == nil {
		t.Fatal("无法识别的目标应当失败")
	}
	if n := strings.Count(console, "下载完成"); n != 1 {
		t.Fatalf("收尾汇总应当恰好打一行，实际 %d 行：%q", n, console)
	}
	if !strings.Contains(console, "⚠ 下载完成   成功 0 · 失败 1 · ") {
		t.Fatalf("收尾汇总的计数与本次运行不符：%q", console)
	}
	// 汇总行是这次运行的结果（内容通道），不带日志时间戳前缀。
	for _, line := range strings.Split(console, "\n") {
		if strings.Contains(line, "下载完成") && summaryStampRe.MatchString(line) {
			t.Errorf("汇总行不该带时间戳前缀：%q", line)
		}
	}

	// 失败目标要登记进**注入的** WorkDir：resume 功能的前提，也是本用例的隔离证据。
	body, err := os.ReadFile(filepath.Join(workDir, pendingFileName))
	if err != nil {
		t.Fatalf("失败目标应当被登记到注入的 WorkDir %s：%v", workDir, err)
	}
	if !strings.Contains(string(body), "这不是一个视频地址") {
		t.Errorf("注入目录里的清单应当含本次失败目标，实际 %q", string(body))
	}
}
