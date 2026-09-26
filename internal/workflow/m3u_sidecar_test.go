package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

func newM3UTestWorkflow() *Workflow {
	return New(config.DefaultMyOption(), util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil))
}

// --write-m3u：产物目录里的 .m3u 侧车。三态（开关关 / 开 / 产物不存在）与 NFO 侧车对齐，
// 直接单测 writeM3USidecar，不依赖真实下载/ffmpeg。
//
// 变异验证：
//   - 去掉 writeM3USidecar 里的 os.WriteFile → 本用例变红；
//   - 把登记的 Duration: page.Dur 改回 m3uUnknownDuration → 「开关打开」段的 300 期望红。
func TestWriteM3USidecar(t *testing.T) {
	dir := t.TempDir()
	product := filepath.Join(dir, "剧.mp4")
	if err := os.WriteFile(product, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	playlist := filepath.Join(dir, "剧.m3u")
	page := entity.Page{Index: 2, Title: "P2 分P", Dur: 300}

	wf := newM3UTestWorkflow()

	// 开关关闭：不写。
	wf.writeM3USidecar(product, "剧", page)
	if _, err := os.Stat(playlist); !os.IsNotExist(err) {
		t.Errorf("--write-m3u 未开时不该写播放列表（err=%v）", err)
	}

	// 开关打开：写，含头部、EXTINF 与产物相对路径。
	// 时长用分P声明的秒数（page.Dur=300 → #EXTINF:300）：播放器据此显示长度——
	// 这正是本次改动钉住的行为（此前一律写 -1，列表里看不出每集多长）。
	wf.Cfg.WriteM3U = true
	wf.writeM3USidecar(product, "剧", page)
	body, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatalf("应当写出 %s：%v", playlist, err)
	}
	for _, want := range []string{"#EXTM3U\n", "#EXTINF:300,P2 分P\n", "剧.mp4\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("播放列表缺少 %q：%s", want, body)
		}
	}

	// 产物不存在：不写、不登记（没有可播放的东西），已有内容原样保留。
	missing := filepath.Join(dir, "不存在.mp4")
	wf.writeM3USidecar(missing, "剧", page)
	after, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(body) {
		t.Errorf("产物不存在时播放列表被改写：%q → %q", body, after)
	}
	if strings.Contains(string(after), "不存在.mp4") {
		t.Errorf("产物不存在时不该登记进播放列表：%s", after)
	}
}

// TestRunResetsM3UState 播放列表状态属于「这一次运行」：Workflow 复用（同一进程连跑多个地址）
// 时不能让上一次的登记混进新列表。这里借一次「URL 为空 → Run 立刻报错」的空跑触发重置，不依赖网络。
//
// 变异验证：删掉 Run 开头的 w.m3u = nil → 本用例红（状态残留）。
func TestRunResetsM3UState(t *testing.T) {
	dir := t.TempDir()
	product := filepath.Join(dir, "旧.mp4")
	if err := os.WriteFile(product, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := newM3UTestWorkflow()
	wf.Cfg.WriteM3U = true
	wf.writeM3USidecar(product, "旧", entity.Page{Index: 1, Title: "旧"})
	if wf.m3u == nil {
		t.Fatal("前置条件：登记后应当已有播放列表状态")
	}
	if err := wf.Run(context.Background()); err == nil {
		t.Fatal("URL 为空时 Run 应当报错（本用例只借这次空跑触发重置）")
	}
	if wf.m3u != nil {
		t.Errorf("Run 开头必须重置播放列表状态，实际仍残留 %+v", wf.m3u)
	}
}

// TestWriteM3USidecarMultiPageOrderAndDedup 多P：按分P顺序、相对产物目录的路径、按路径去重。
// 默认多P模板把产物放在 <videoTitle>/ 下，播放列表就落在那个目录里，条目是相对它的文件名。
//
// 变异验证：
//   - 去掉 sortM3UEntries（按登记顺序直接渲染）→ 乱序登记导致本用例红；
//   - 去掉 AppendM3UEntry 的按路径去重 → 重复行断言红。
func TestWriteM3USidecarMultiPageOrderAndDedup(t *testing.T) {
	dir := t.TempDir()
	show := filepath.Join(dir, "剧")
	if err := os.MkdirAll(show, 0o755); err != nil {
		t.Fatal(err)
	}
	products := map[int]string{
		1: filepath.Join(show, "[P01]第一话.mp4"),
		2: filepath.Join(show, "[P02]第二话.mp4"),
		3: filepath.Join(show, "[P03]第三话.mp4"),
	}
	titles := map[int]string{1: "第一话", 2: "第二话", 3: "第三话"}
	// 这些分P都没声明时长（Dur 为零值）→ 渲染层回落 #EXTINF:-1：未知时长仍走 -1 分支，
	// 与渲染层纯函数用例（TestRenderM3U 的「时长：未知写 -1，已知写秒」）保持一致。
	for _, p := range products {
		if err := os.WriteFile(p, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	wf := newM3UTestWorkflow()
	wf.Cfg.WriteM3U = true
	// 故意乱序登记（P3 → P1 → P2），并重复登记 P2。
	for _, idx := range []int{3, 1, 2, 2} {
		wf.writeM3USidecar(products[idx], "剧", entity.Page{Index: idx, Title: titles[idx]})
	}

	playlist := filepath.Join(show, "剧.m3u")
	body, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatalf("应当写出 %s：%v", playlist, err)
	}
	want := "#EXTM3U\n" +
		"#EXTINF:-1,第一话\n[P01]第一话.mp4\n" +
		"#EXTINF:-1,第二话\n[P02]第二话.mp4\n" +
		"#EXTINF:-1,第三话\n[P03]第三话.mp4\n"
	if string(body) != want {
		t.Errorf("多P播放列表 = %q, want %q", body, want)
	}
}

// TestWriteM3USidecarFailureOnlyWarns 播放列表写不出去（同名目录挡路）时只告警：
// 不 panic、不动产物；失败原因消失后重试能把列表补上（失败那次的登记不丢）。
//
// 变异验证：把 writeM3USidecar 的失败分支改成 panic(err) → 本用例红；
// 去掉 LogWarn（静默 return）→ 告警断言红。
func TestWriteM3USidecarFailureOnlyWarns(t *testing.T) {
	dir := t.TempDir()
	product := filepath.Join(dir, "剧.mp4")
	if err := os.WriteFile(product, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 用同名目录挡住 os.WriteFile。
	blocked := filepath.Join(dir, "剧.m3u")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	wf := newM3UTestWorkflow()
	wf.Cfg.WriteM3U = true
	page := entity.Page{Index: 1, Title: "P1"}

	out := captureStdout(t, func() {
		wf.writeM3USidecar(product, "剧", page)
	})
	if !strings.Contains(out, "写入播放列表失败") {
		t.Errorf("写入失败必须有可读告警，实际输出：%q", out)
	}
	if got, err := os.ReadFile(product); err != nil || string(got) != "payload" {
		t.Errorf("播放列表写入失败不得动产物：err=%v 内容=%q", err, got)
	}

	// 障碍移除后重试：仍然只有一条（失败那次的登记没有丢，也没有被重复追加）。
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	wf.writeM3USidecar(product, "剧", page)
	body, err := os.ReadFile(blocked)
	if err != nil {
		t.Fatalf("重试应当写出播放列表：%v", err)
	}
	// 这个分P也没给时长（Dur 为零值）→ 未知分支的 -1。
	if want := "#EXTM3U\n#EXTINF:-1,P1\n剧.mp4\n"; string(body) != want {
		t.Errorf("重试后的播放列表 = %q, want %q", body, want)
	}
}

// TestWriteM3USidecarMergesExistingPlaylist 「-p 1」重跑不能把已有列表覆写成只含 P1：
// 第二次运行（新 Workflow = 新一次 Run）只登记 P1，文件里必须仍是三条、按分P顺序。
//
// 旧条目的元数据必须跟着回来：第一次运行已经拿到时长的 P2/P3 不能被合并打回 -1；第一次
// 没拿到时长的 P1 这次拿到了，就该把 -1 刷成真实秒数——否则 2.10.0 之前写出的列表永远显示不出长度。
//
// 变异验证：
//   - 去掉 writeM3USidecar 里的 existing: readM3UEntries(...) → 本用例红（P2/P3 丢了）；
//   - 去掉 parseM3U 的 #EXTINF 解析 → 本用例红（302/303 变成 -1）；
//   - 去掉 mergeM3UEntries 的 oldPos 分支（重复路径也走 AppendM3UEntry）→ 本用例红（P1 仍是 -1）。
func TestWriteM3USidecarMergesExistingPlaylist(t *testing.T) {
	dir := t.TempDir()
	show := filepath.Join(dir, "剧")
	if err := os.MkdirAll(show, 0o755); err != nil {
		t.Fatal(err)
	}
	products := map[int]string{
		1: filepath.Join(show, "[P01]第一话.mp4"),
		2: filepath.Join(show, "[P02]第二话.mp4"),
		3: filepath.Join(show, "[P03]第三话.mp4"),
	}
	titles := map[int]string{1: "第一话", 2: "第二话", 3: "第三话"}
	for _, p := range products {
		if err := os.WriteFile(p, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 第一次运行：P1 没拿到时长（Dur=0 → 未知），P2/P3 拿到了。
	first := newM3UTestWorkflow()
	first.Cfg.WriteM3U = true
	first.writeM3USidecar(products[1], "剧", entity.Page{Index: 1, Title: titles[1]})
	first.writeM3USidecar(products[2], "剧", entity.Page{Index: 2, Title: titles[2], Dur: 302})
	first.writeM3USidecar(products[3], "剧", entity.Page{Index: 3, Title: titles[3], Dur: 303})

	// 第二次运行（-p 1）：只登记 P1，这次拿到了真实时长。
	second := newM3UTestWorkflow()
	second.Cfg.WriteM3U = true
	second.writeM3USidecar(products[1], "剧", entity.Page{Index: 1, Title: titles[1], Dur: 301})

	playlist := filepath.Join(show, "剧.m3u")
	want := "#EXTM3U\n" +
		"#EXTINF:301,第一话\n[P01]第一话.mp4\n" +
		"#EXTINF:302,第二话\n[P02]第二话.mp4\n" +
		"#EXTINF:303,第三话\n[P03]第三话.mp4\n"
	assertPlaylist(t, playlist, want)

	body, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"[P01]第一话.mp4", "[P02]第二话.mp4", "[P03]第三话.mp4"} {
		if n := strings.Count(string(body), p+"\n"); n != 1 {
			t.Errorf("%s 应恰好出现一次，实际 %d 次：\n%s", p, n, body)
		}
	}
}

// TestWriteM3USidecarMergeDedupesByPath 合并按路径去重：旧文件里同一路径出现两次（手工编辑/
// 历史写入）只留首次出现的那条，本次运行再登记同一路径也不追加第二行，元数据用本次的刷新。
//
// 变异验证：去掉 mergeM3UEntries 对 existing 的 seen 去重 → 本用例红（写出了两行 b.mp4）。
func TestWriteM3USidecarMergeDedupesByPath(t *testing.T) {
	dir := t.TempDir()
	product := filepath.Join(dir, "b.mp4")
	if err := os.WriteFile(product, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	playlist := filepath.Join(dir, "剧.m3u")
	old := "#EXTM3U\n" +
		"#EXTINF:100,旧标题一\nb.mp4\n" +
		"#EXTINF:100,旧标题二\nb.mp4\n"
	if err := os.WriteFile(playlist, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	wf := newM3UTestWorkflow()
	wf.Cfg.WriteM3U = true
	wf.writeM3USidecar(product, "剧", entity.Page{Index: 2, Title: "第二话", Dur: 250})

	body, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#EXTM3U\n#EXTINF:250,第二话\nb.mp4\n"; string(body) != want {
		t.Errorf("合并后的播放列表 = %q, want %q", body, want)
	}
	if n := strings.Count(string(body), "b.mp4\n"); n != 1 {
		t.Errorf("同一路径只应留一行，实际 %d 行：\n%s", n, body)
	}
}

// TestWriteM3USidecarCorruptPlaylistTreatedAsEmpty 读回损坏的旧列表（没有 #EXTM3U 头）不能中断，
// 也不能把里面的行当路径并进新列表：当空列表处理、按本次条目重写；--debug 下留一行线索。
//
// 变异验证：
//   - 去掉 parseM3U 的头部校验（无头也算合法）→ 本用例红（垃圾行被当成路径写进列表）；
//   - 去掉 readM3UEntries 损坏分支的 LogDebug → debug 断言红。
func TestWriteM3USidecarCorruptPlaylistTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	products := map[int]string{
		1: filepath.Join(dir, "[P01]第一话.mp4"),
		2: filepath.Join(dir, "[P02]第二话.mp4"),
	}
	for _, p := range products {
		if err := os.WriteFile(p, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	playlist := filepath.Join(dir, "剧.m3u")
	// 损坏：半截写入 / 被别的程序覆盖——没有 #EXTM3U 头。
	if err := os.WriteFile(playlist, []byte("\x00\x01 半截内容\nnot a playlist\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	util.SetDefaultDebugFn(func() bool { return true })
	t.Cleanup(func() { util.SetDefaultDebugFn(func() bool { return false }) })

	wf := newM3UTestWorkflow()
	wf.Cfg.WriteM3U = true
	out := captureStdout(t, func() {
		wf.writeM3USidecar(products[1], "剧", entity.Page{Index: 1, Title: "第一话", Dur: 100})
		wf.writeM3USidecar(products[2], "剧", entity.Page{Index: 2, Title: "第二话", Dur: 200})
	})
	if !strings.Contains(out, "播放列表损坏") {
		t.Errorf("损坏的旧列表应当在 --debug 下留一行线索，实际输出：%q", out)
	}
	want := "#EXTM3U\n" +
		"#EXTINF:100,第一话\n[P01]第一话.mp4\n" +
		"#EXTINF:200,第二话\n[P02]第二话.mp4\n"
	assertPlaylist(t, playlist, want)
}
