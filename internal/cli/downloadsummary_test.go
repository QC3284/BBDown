package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown-Go/internal/config"
	"github.com/QC3284/BBDown-Go/internal/util"
	"github.com/spf13/cobra"
)

// 下载结束的收尾汇总（任务 G-2）：downloadTargets 整批跑完时打一行「成功/失败任务数 + 耗时」。
// 产物文件数目前拿不到（见 batchSummary.products 的说明），所以只报前两项。

// TestFormatBatchSummary 是纯函数表：输入计数/耗时 → 期望字符串。
//
// 变异验证：耗时改成不取整（直接 %v）→ 第三条表的 2.4s 变红；去掉产出文件数分支 → 第三条变红。
func TestFormatBatchSummary(t *testing.T) {
	cases := []struct {
		name  string
		stats batchSummary
		want  string
	}{
		{
			"全部成功",
			batchSummary{succeeded: 2, failed: 0, elapsed: 1500 * time.Millisecond, products: productsUnknown},
			"下载完成：成功 2 个，失败 0 个，耗时 1.5s",
		},
		{
			"部分失败（耗时按 100ms 取整）",
			batchSummary{succeeded: 1, failed: 2, elapsed: 65*time.Second + 234*time.Millisecond, products: productsUnknown},
			"下载完成：成功 1 个，失败 2 个，耗时 1m5.2s",
		},
		{
			"有产出列表时报文件数",
			batchSummary{succeeded: 1, failed: 0, elapsed: 2400 * time.Millisecond, products: 4},
			"下载完成：成功 1 个，失败 0 个，耗时 2.4s，产出 4 个文件",
		},
		{
			"零目标",
			batchSummary{succeeded: 0, failed: 0, elapsed: 0, products: productsUnknown},
			"下载完成：成功 0 个，失败 0 个，耗时 0s",
		},
		{
			"有字节数时补平均速率（BBDownT 口径：耗时 · 平均 Y MB/s）",
			batchSummary{succeeded: 2, failed: 0, elapsed: 1500 * time.Millisecond, bytes: 10 << 20, products: productsUnknown},
			"下载完成：成功 2 个，失败 0 个，耗时 1.5s · 平均 6.7 MB/s", // 10MiB/1.5s = 6.67 MiB/s
		},
		{
			"慢速时降到 KB/s（与进度行同一套单位）",
			batchSummary{succeeded: 1, failed: 0, elapsed: 10 * time.Second, bytes: 100 << 10, products: productsUnknown},
			"下载完成：成功 1 个，失败 0 个，耗时 10s · 平均 10 KB/s",
		},
		{
			"拿不到字节数（0）时不报平均速率——不虚报",
			batchSummary{succeeded: 1, failed: 0, elapsed: 2400 * time.Millisecond, bytes: 0, products: productsUnknown},
			"下载完成：成功 1 个，失败 0 个，耗时 2.4s",
		},
	}
	for _, tc := range cases {
		if got := formatBatchSummary(tc.stats); got != tc.want {
			t.Errorf("%s：formatBatchSummary = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// TestFormatAverageSpeed 钉住平均速率的口径：字节/耗时，单位与进度行一致（MB 一位小数、
// KB/B 取整），拿不到字节数或耗时为 0 时**不报**（返回空串，汇总里就没有那一段）。
//
// 耗时与字节数都是入参——用例注入，不读墙钟，所以这条断言在任何 runner 上都稳定。
//
// 变异验证：把 bytes<=0 的守卫去掉 → 第三条（0 字节）变红，汇总行会多出「平均 0 B/s」。
func TestFormatAverageSpeed(t *testing.T) {
	cases := []struct {
		name    string
		bytes   int64
		elapsed time.Duration
		want    string
	}{
		{"MB 档（一位小数）", 10 << 20, 1500 * time.Millisecond, "6.7 MB/s"},
		{"KB 档", 100 << 10, 10 * time.Second, "10 KB/s"},
		{"B 档", 500, 10 * time.Second, "50 B/s"},
		{"没有字节数：不报", 0, 10 * time.Second, ""},
		{"耗时为 0：不报（避免除零造出 Inf）", 1024, 0, ""},
	}
	for _, tc := range cases {
		if got := formatAverageSpeed(tc.bytes, tc.elapsed); got != tc.want {
			t.Errorf("%s：formatAverageSpeed = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// TestDownloadTargetsPrintsBatchSummary 跑一次真实的 downloadTargets：目标无法识别（走本地
// 错误路径，不碰网络），断言收尾汇总那一行确实打了出来，且计数与本次运行一致。
//
// WorkDir 必须注入临时目录：失败目标会被登记进 .bbdown-pending.json，而 pendingPath 在
// WorkDir 为空时退回**进程工作目录**（对真实用户是设计，对测试就是往仓库里写状态文件）。
// 包级守卫见 workdir_guard_test.go 的 TestMain。
//
// 变异验证：删掉 downloadTargets 里那次 util.Log(formatBatchSummary…) → 本用例变红；
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
	if n := strings.Count(console, "下载完成："); n != 1 {
		t.Fatalf("收尾汇总应当恰好打一行，实际 %d 行：%q", n, console)
	}
	if !strings.Contains(console, "下载完成：成功 0 个，失败 1 个，耗时 ") {
		t.Fatalf("收尾汇总的计数与本次运行不符：%q", console)
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

// TestByteAccumulatorDedupsByPath 钉住汇总字节的按路径去重：分片重试/断点续传会重复调用
// OnSaved，同一路径只应计一次（与 serve 的 onArtifactSaved 同一语义）。
//
// 变异验证：把 LoadOrStore 的去重分支删掉（恒统计）→ 重复上报用例变红。
func TestByteAccumulatorDedupsByPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatalf("写产物: %v", err)
	}

	acc := &byteAccumulator{}
	if n := acc.add(path); n != 5 {
		t.Fatalf("首次上报应计入 5 字节，实际 %d", n)
	}
	if n := acc.add(path); n != 0 {
		t.Fatalf("同路径重复上报应去重（返回 0），实际 %d", n)
	}
	path2 := filepath.Join(dir, "sub.srt")
	if err := os.WriteFile(path2, []byte("abcd"), 0o644); err != nil {
		t.Fatalf("写侧车: %v", err)
	}
	if n := acc.add(path2); n != 4 {
		t.Fatalf("不同路径应独立计入 4 字节，实际 %d", n)
	}
	if n := acc.add(filepath.Join(dir, "ghost.mp4")); n != 0 {
		t.Fatalf("stat 失败应返回 0，实际 %d", n)
	}
}
