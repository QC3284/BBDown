package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QC3284/BBDown-Go/internal/entity"
	"github.com/QC3284/BBDown-Go/internal/substore"
	"github.com/QC3284/BBDown-Go/internal/util"
)

// ---- t47：--per-sub-dir / 增量扫描（fetchResolved）/ --full-scan 的编排回归 ----
//
// 这三件事都在 subCheckDeps 的可注入边界上，用例因此不碰真网、不写包目录：
// 目录布局逐字断言，新依赖的优先级两臂断言，与 --since/--concurrency 的正交性用组合用例钉住。

// TestSubCheckWorkDirLayout 逐字钉住 --per-sub-dir 的目录布局：
// 关闭（plan 为空）时就是 work-dir 原样；打开时是 <work-dir>/<订阅名>（重名带 -2）。
func TestSubCheckWorkDirLayout(t *testing.T) {
	wd := filepath.Join("work")
	subA := substore.Subscription{Target: "mid:1", Name: "UP-A"}
	subB := substore.Subscription{Target: "mid:2", Name: "UP-A"}
	subC := substore.Subscription{Target: "mid:3", Name: "另一条"}

	if got := subCheckWorkDir(wd, nil, subA); got != wd {
		t.Errorf("默认关闭时工作目录应当是 %q，实际 %q", wd, got)
	}
	plan := substore.PlanSubDirs([]substore.Subscription{subA, subB, subC})
	if got := subCheckWorkDir(wd, plan, subA); got != filepath.Join(wd, "UP-A") {
		t.Errorf("第一条订阅的目录 = %q, want %q", got, filepath.Join(wd, "UP-A"))
	}
	if got := subCheckWorkDir(wd, plan, subB); got != filepath.Join(wd, "UP-A-2") {
		t.Errorf("重名订阅的目录 = %q, want %q", got, filepath.Join(wd, "UP-A-2"))
	}
	if got := subCheckWorkDir(wd, plan, subC); got != filepath.Join(wd, "另一条") {
		t.Errorf("第三条订阅的目录 = %q, want %q", got, filepath.Join(wd, "另一条"))
	}
	// 规划表里没有的 target（理论上进不了调度）退回 work-dir，而不是拼出空目录名。
	if got := subCheckWorkDir(wd, plan, substore.Subscription{Target: "mid:999"}); got != wd {
		t.Errorf("未规划的 target 应当退回 %q，实际 %q", wd, got)
	}
	// 净化后的目录名里不含分隔符：整条路径只有 work-dir 与一层订阅目录。
	dirty := substore.Subscription{Target: "mid:7", Name: "a/b"}
	got := subCheckWorkDir(wd, substore.PlanSubDirs([]substore.Subscription{dirty}), dirty)
	if filepath.Dir(got) != wd {
		t.Errorf("订阅目录必须是 work-dir 的直接子目录：%q（work-dir=%q）", got, wd)
	}
}

// depsRecorder 记录两种下载口径各自被调用的次数与参数（并发安全）。
type depsRecorder struct {
	mu        sync.Mutex
	legacy    []string
	newDeps   []string
	legaCalls int
	newCalls  int
}

func (r *depsRecorder) recordLegacy(aid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.legacy = append(r.legacy, aid)
	r.legaCalls++
}

func (r *depsRecorder) recordNew(target, aid, dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.newDeps = append(r.newDeps, fmt.Sprintf("%s|%s|%s", target, aid, dir))
	r.newCalls++
}

func (r *depsRecorder) snapshot() (legacyCalls, newCalls int, newDeps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.legaCalls, r.newCalls, append([]string(nil), r.newDeps...)
}

// TestSubCheckPrefersNewDeps 两臂钉住依赖优先级：填了新字段就用新的（t47 的
// fetchResolved/downloadInto），没填就仍然走旧字段（默认路径与改前一致）。
func TestSubCheckPrefersNewDeps(t *testing.T) {
	setSubStoreRoot(t, t.TempDir())
	subs := []substore.Subscription{{Target: "mid:1", Name: "UP-A"}}
	now := time.Now()

	vInfo := func() *entity.VInfo {
		return &entity.VInfo{Title: "UP-A", PagesInfo: []entity.Page{{Aid: "170001", PubTime: now.Add(-time.Hour).Unix()}}}
	}

	// 臂一：新字段存在 → 旧字段一次都不该被调。
	rec := &depsRecorder{}
	depsNew := subCheckDeps{
		resolve: func(context.Context, string) (string, error) { return "mid:1", nil },
		fetch: func(context.Context, string) (*entity.VInfo, error) {
			t.Error("应当优先 fetchResolved，而不是 fetch")
			return vInfo(), nil
		},
		download: func(context.Context, string) error {
			t.Error("应当优先 downloadInto，而不是 download")
			return nil
		},
		fetchResolved: func(_ context.Context, _ substore.Subscription, resolved string) (*entity.VInfo, error) {
			if resolved != "mid:1" {
				t.Errorf("fetchResolved 收到 resolved=%q, want mid:1", resolved)
			}
			return vInfo(), nil
		},
		downloadInto: func(_ context.Context, sub substore.Subscription, aid string) error {
			rec.recordNew(sub.Target, aid, subCheckWorkDir("wd", nil, sub))
			return nil
		},
	}
	failures, err := subCheckRun(context.Background(), subs, depsNew, subCheckSchedule{Concurrency: 1}, now)
	if err != nil || failures != 0 {
		t.Fatalf("subCheckRun: failures=%d err=%v", failures, err)
	}
	legacyCalls, newCalls, records := rec.snapshot()
	if legacyCalls != 0 || newCalls != 1 {
		t.Errorf("新字段应当各用一次、旧字段零次：legacy=%d new=%d", legacyCalls, newCalls)
	}
	if len(records) != 1 || records[0] != "mid:1|170001|wd" {
		t.Errorf("downloadInto 收到 (%v), want [mid:1|170001|wd]", records)
	}

	// 臂二：只有旧字段 → 走旧路径（这正是既有用例覆盖的形状，这里显式钉住）。
	// 换一个空历史：臂一已经把这个 aid 记进历史，否则这里会「没有新增内容」而不下载。
	setSubStoreRoot(t, t.TempDir())
	rec2 := &depsRecorder{}
	depsOld := subCheckDeps{
		resolve:  func(context.Context, string) (string, error) { return "mid:1", nil },
		fetch:    func(context.Context, string) (*entity.VInfo, error) { return vInfo(), nil },
		download: func(_ context.Context, aid string) error { rec2.recordLegacy(aid); return nil },
	}
	if _, err := subCheckRun(context.Background(), subs, depsOld, subCheckSchedule{Concurrency: 1}, now); err != nil {
		t.Fatalf("旧路径 subCheckRun: %v", err)
	}
	legacyCalls2, newCalls2, _ := rec2.snapshot()
	if legacyCalls2 != 1 || newCalls2 != 0 {
		t.Errorf("旧路径应当用 download 一次：legacy=%d new=%d", legacyCalls2, newCalls2)
	}
	if len(rec2.legacy) != 1 || rec2.legacy[0] != "170001" {
		t.Errorf("download 收到 %v, want [170001]", rec2.legacy)
	}
}

// TestSubCheckPerSubDirWithSinceAndConcurrency 是正交性用例：--per-sub-dir 与
// --since / --concurrency 组合时，三条语义各就各位——
// 窗口外的旧稿仍被跳过、检查仍可并发、下载仍按订阅顺序且各自落在自己的子目录。
func TestSubCheckPerSubDirWithSinceAndConcurrency(t *testing.T) {
	setSubStoreRoot(t, t.TempDir())
	subs := []substore.Subscription{
		{Target: "mid:1", Name: "UP-A"},
		{Target: "mid:2", Name: "UP-A"}, // 同名 → 目录应当让开成 UP-A-2
	}
	now := time.Now()
	plan := substore.PlanSubDirs(subs)
	wd := filepath.Join("work")

	pages := map[string]*entity.VInfo{
		"mid:1": {Title: "UP-A", PagesInfo: []entity.Page{
			{Aid: "170001", PubTime: now.Add(-1 * time.Hour).Unix()},   // 窗口内
			{Aid: "170002", PubTime: now.Add(-100 * time.Hour).Unix()}, // 窗口外（--since 24h 应跳过）
		}},
		"mid:2": {Title: "UP-A", PagesInfo: []entity.Page{
			{Aid: "170003", PubTime: now.Add(-2 * time.Hour).Unix()},
		}},
	}

	rec := &depsRecorder{}
	deps := subCheckDeps{
		resolve: func(_ context.Context, target string) (string, error) { return target, nil },
		fetch: func(_ context.Context, resolved string) (*entity.VInfo, error) {
			v, ok := pages[resolved]
			if !ok {
				return nil, fmt.Errorf("未知目标 %s", resolved)
			}
			return v, nil
		},
		download: func(context.Context, string) error {
			t.Error("开了 --per-sub-dir 就不该走旧下载口径")
			return nil
		},
		downloadInto: func(_ context.Context, sub substore.Subscription, aid string) error {
			rec.recordNew(sub.Target, aid, subCheckWorkDir(wd, plan, sub))
			return nil
		},
	}
	sched := subCheckSchedule{Since: 24 * time.Hour, Concurrency: 2}

	var buf strings.Builder
	restore := util.RedirectConsoleLogs(&buf)
	failures, err := subCheckRun(context.Background(), subs, deps, sched, now)
	restore()
	if err != nil || failures != 0 {
		t.Fatalf("subCheckRun: failures=%d err=%v", failures, err)
	}
	logs := buf.String()

	_, newCalls, records := rec.snapshot()
	want := []string{
		"mid:1|170001|" + filepath.Join(wd, "UP-A"),
		"mid:2|170003|" + filepath.Join(wd, "UP-A-2"),
	}
	if newCalls != 2 || fmt.Sprint(records) != fmt.Sprint(want) {
		t.Errorf("下载记录 = %v, want %v", records, want)
	}
	joined := logs
	if !strings.Contains(joined, "跳过 1 个超出 --since") {
		t.Errorf("--since 窗口应当仍然生效（跳过 1 个旧稿），实际日志：%v", logs)
	}
	if !strings.Contains(joined, "并行检查 2 个订阅") {
		t.Errorf("--concurrency 2 应当仍然走并行检查，实际日志：%v", logs)
	}
}

// TestSubCheckEnhancementFlagDefaults 钉住两个新开关的**名字与默认值**（默认都关闭）：
// 名字打错会让用户拿到 unknown flag，默认值打错会让「不开参数 = 与改前一致」这条失效。
func TestSubCheckEnhancementFlagDefaults(t *testing.T) {
	for _, name := range []string{"per-sub-dir", "full-scan"} {
		flag := subCheckCmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("sub check 缺少 --%s 参数", name)
		}
		if flag.DefValue != "false" {
			t.Errorf("--%s 的默认值应当是 false（不开参数 = 与改前一致），实际 %q", name, flag.DefValue)
		}
	}
	// 旧参数一个都不能少（正交性：新参数不替代 --since/--concurrency）。
	for _, name := range []string{"since", "concurrency"} {
		if subCheckCmd.Flags().Lookup(name) == nil {
			t.Errorf("sub check 丢了既有参数 --%s", name)
		}
	}
}
