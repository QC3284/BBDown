package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/substore"
)

// ---- 清单重复 Target 去重（qa 探针：手改清单 + --concurrency≥2 会重复下载同一批新稿）----
//
// 全部离线：清单直接写 JSON（sub add 同 Target 是替换，正常用法造不出重复），依赖用
// subCheckDeps 注入的假实现，不碰真网、不写包目录。

// writeManifest 直接写一份订阅清单，含重复 Target 也照写——这就是「手改清单」的形态。
func writeManifest(t *testing.T, dir string, targets []string) {
	t.Helper()
	subs := make([]substore.Subscription, 0, len(targets))
	for i, target := range targets {
		subs = append(subs, substore.Subscription{
			Target:  target,
			Name:    fmt.Sprintf("订阅%d", i+1),
			AddedAt: int64(i + 1),
		})
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		t.Fatalf("序列化清单: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "BBDownSubscriptions.json"), data, 0o644); err != nil {
		t.Fatalf("写清单: %v", err)
	}
}

// joinedTargets 把订阅目标拼成逗号串，便于比较顺序。
func joinedTargets(subs []substore.Subscription) string {
	targets := make([]string, 0, len(subs))
	for _, s := range subs {
		targets = append(targets, s.Target)
	}
	return strings.Join(targets, ",")
}

// TestDedupSubscriptions 纯函数语义：保留**先出现**的条目（含它的 Name 等字段）与原顺序，
// 重复项按原顺序单独返回；无重复时原样返回（空清单、单条、多 Target 都不变形状）。
//
// 变异验证：把去重分支删掉（恒返回全部）→ 重复项用例变红。
func TestDedupSubscriptions(t *testing.T) {
	cases := []struct {
		name     string
		in       []substore.Subscription
		wantKept string
		wantDups string
	}{
		{"无重复", []substore.Subscription{{Target: "a"}, {Target: "b"}}, "a,b", ""},
		{"空清单", nil, "", ""},
		{"单条", []substore.Subscription{{Target: "a"}}, "a", ""},
		{"两条同 Target", []substore.Subscription{{Target: "a", Name: "先"}, {Target: "a", Name: "后"}}, "a", "a"},
		{"qa 探针形态", []substore.Subscription{{Target: "a"}, {Target: "b"}, {Target: "a"}, {Target: "b"}}, "a,b", "a,b"},
		{"三连重复", []substore.Subscription{{Target: "a"}, {Target: "a"}, {Target: "a"}}, "a", "a,a"},
		{"重复夹在中间", []substore.Subscription{{Target: "a"}, {Target: "b"}, {Target: "a"}, {Target: "c"}}, "a,b,c", "a"},
	}
	for _, c := range cases {
		kept, dups := dedupSubscriptions(c.in)
		if got := joinedTargets(kept); got != c.wantKept {
			t.Errorf("%s: 保留的订阅 got %q want %q", c.name, got, c.wantKept)
		}
		if got := joinedTargets(dups); got != c.wantDups {
			t.Errorf("%s: 重复项 got %q want %q", c.name, got, c.wantDups)
		}
	}

	// 保留的是**先出现**的那一条：Name 取第一条，不是最后一条。
	kept, _ := dedupSubscriptions([]substore.Subscription{{Target: "a", Name: "先"}, {Target: "a", Name: "后"}})
	if len(kept) != 1 || kept[0].Name != "先" {
		t.Fatalf("去重必须保留先出现的那一条（Name 应为「先」）: %+v", kept)
	}
}

// TestSubCheckDedupedLogsDuplicates：日志点名被去重的重复项；无重复时**一行都不打**
// （「清单没毛病」的运行输出必须一字不改）。
func TestSubCheckDedupedLogsDuplicates(t *testing.T) {
	quiet := captureStdout(t, func() {
		kept := subCheckDeduped([]substore.Subscription{{Target: "a"}, {Target: "b"}})
		if joinedTargets(kept) != "a,b" {
			t.Fatalf("无重复时必须原样返回: %q", joinedTargets(kept))
		}
	})
	if quiet != "" {
		t.Errorf("无重复时不该有任何输出（默认观感是契约）: %q", quiet)
	}

	noisy := captureStdout(t, func() {
		kept := subCheckDeduped([]substore.Subscription{{Target: "a"}, {Target: "b"}, {Target: "a"}, {Target: "b"}})
		if joinedTargets(kept) != "a,b" {
			t.Fatalf("去重结果: %q", joinedTargets(kept))
		}
	})
	msgs := strings.Join(logMessages(noisy), "\n")
	if !strings.Contains(msgs, "重复 Target") {
		t.Errorf("重复 Target 要留一条日志: %q", msgs)
	}
	if !strings.Contains(msgs, "a, b") {
		t.Errorf("日志要点名被去重的 Target（a, b）: %q", msgs)
	}
}

// TestSubCheckDuplicatedManifestDownloadsOnce 是 qa 探针的离线复现：
//
//	清单 [mid:1, mid:2]      + 默认（串行）→ 下载 1,2
//	清单 [mid:1, mid:2, mid:1, mid:2] + --concurrency 2 → 仍然只有 1,2（每个 aid 一次）
//
// 这条链就是 runSubCheck 的接线（手改清单 → Load → subCheckDeduped → 调度），只跳过了
// 「建立会话」这段与去重无关、且需要真网的部分；接线本身由
// TestRunSubCheckDedupsManifestBeforeScheduling 用已取消的 ctx 钉住。
//
// 变异验证：把 dedupSubscriptions 的去重分支删掉（恒返回全部）→ 这里会拿到 1,2,1,2，断言红。
func TestSubCheckDuplicatedManifestDownloadsOnce(t *testing.T) {
	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	infos := map[string]*entity.VInfo{
		"mid:1": vinfo("甲", []string{"1"}, nil),
		"mid:2": vinfo("乙", []string{"2"}, nil),
	}

	// runOnce 跑一次「手改清单 → 去重 → 调度」，返回下载顺序与捕获到的输出。
	runOnce := func(t *testing.T, targets []string, concurrency int) (string, string) {
		t.Helper()
		dir := t.TempDir()
		setSubStoreRoot(t, dir)
		writeManifest(t, dir, targets)

		loaded, err := substore.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(loaded) != len(targets) {
			t.Fatalf("手改清单应当原样读入 %d 条，实际 %d", len(targets), len(loaded))
		}

		dl, downloads := recordingDownload()
		out := captureStdout(t, func() {
			subs := subCheckDeduped(loaded)
			failures, err := subCheckRun(context.Background(), subs, fakeDeps(infos, dl),
				subCheckSchedule{Concurrency: concurrency}, now)
			if err != nil || failures != 0 {
				t.Fatalf("跑失败: failures=%d err=%v", failures, err)
			}
		})
		return strings.Join(downloads(), ","), out
	}

	base, _ := runOnce(t, []string{"mid:1", "mid:2"}, 1)
	if base != "1,2" {
		t.Fatalf("基准（每个 Target 一条、串行）应当是 1,2，实际 %q", base)
	}

	got, out := runOnce(t, []string{"mid:1", "mid:2", "mid:1", "mid:2"}, 2)
	if got != base {
		t.Fatalf("重复 Target 清单 + --concurrency 2 必须与不重复时下载同样多（每个 aid 只下载一次）: got %q want %q", got, base)
	}
	seen := map[string]int{}
	for _, aid := range strings.Split(got, ",") {
		seen[aid]++
	}
	for aid, n := range seen {
		if n != 1 {
			t.Errorf("aid %s 被下载了 %d 次（重复下载就是这次要修的缺陷）", aid, n)
		}
	}
	// 日志点名被去重的两条（mid:1、mid:2 各多出一条）。
	msgs := strings.Join(logMessages(out), "\n")
	if !strings.Contains(msgs, "重复 Target") || !strings.Contains(msgs, "mid:1, mid:2") {
		t.Errorf("要点名被去重的重复项: %q", msgs)
	}
	// 历史里每个 Target 只有它自己那一条。
	assertHistory(t, "mid:1", "1")
	assertHistory(t, "mid:2", "2")
}

// TestRunSubCheckDedupsManifestBeforeScheduling 钉住接线本身：runSubCheck 必须在
// substore.Load() 之后、进入调度（建会话/检查/下载）之前就去重并报出来。
//
// 做法：用**已取消的 ctx** 把整条路径收进一个离线用例——HTTP 客户端在 ctx 已取消时
// 不会真的发请求（net/http 的 Transport 先查 ctx.Done）；InitSession 在无凭据时打
// 「你尚未登录B站账号！」后返回 nil（不联网），随后 subCheckRun 走取消路径返回 Canceled，
// 既不联网也不下载任何东西；而 runSubCheck 的去重日志出现在建会话之前，所以输出里
// 只要能看到它，就说明去重确实在调度之前发生。清单目录的签名还必须逐字节不变
// （没有历史文件写出来）。
//
// 变异验证：把 runSubCheck 里的 subCheckDeduped 调用删掉 → 本用例的日志断言变红；
// 把返回值丢弃（_ = subCheckDeduped(subs)）→ 「并行检查 1 个订阅」断言变红
// （重复下载那条用例只覆盖去重逻辑本身，接线与移交由这里守）。
func TestRunSubCheckDedupsManifestBeforeScheduling(t *testing.T) {
	dir := t.TempDir()
	setSubStoreRoot(t, dir)
	writeManifest(t, dir, []string{"mid:1", "mid:1"})
	before := dirSignature(t, dir)

	optSubCheckSince, optSubCheckConcurrency = "", 2
	t.Cleanup(func() { optSubCheckSince, optSubCheckConcurrency = "", 1 })

	// 已取消的 ctx：会话建立阶段不会联网（见用例注释），也不会走到任何下载。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	subCheckCmd.SetContext(ctx)
	t.Cleanup(func() { subCheckCmd.SetContext(nil) })

	out := captureStdout(t, func() {
		if err := runSubCheck(subCheckCmd, nil); err == nil {
			t.Fatal("ctx 已取消时 runSubCheck 必须走取消路径报错，而不是当作跑完")
		}
	})

	msgs := strings.Join(logMessages(out), "\n")
	if !strings.Contains(msgs, "重复 Target") || !strings.Contains(msgs, "mid:1") {
		t.Fatalf("runSubCheck 必须在调度之前去重并点名重复项，实际输出: %q", msgs)
	}
	// 钉住「去重**结果**真的交给了调度」：清单 [mid:1,mid:1] 去重后应只剩 1 个订阅，
	// 并发横幅必须说「并行检查 1 个订阅」——把 runSubCheck 里的调用改成丢弃返回值
	// （_ = subCheckDeduped(subs)，保留日志）时横幅会说 2 个，这条断言会红（qa 实测）。
	if !strings.Contains(msgs, "并行检查 1 个订阅") {
		t.Fatalf("去重结果必须真正交给调度（[mid:1,mid:1] 去重后应只并行检查 1 个订阅），实际输出: %q", msgs)
	}
	if after := dirSignature(t, dir); after != before {
		t.Fatalf("取消的运行不得写任何状态文件：\nbefore=%s\nafter=%s", before, after)
	}
}
