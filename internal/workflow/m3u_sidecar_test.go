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
// 变异验证：去掉 writeM3USidecar 里的 os.WriteFile → 本用例变红。
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
	wf.Cfg.WriteM3U = true
	wf.writeM3USidecar(product, "剧", page)
	body, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatalf("应当写出 %s：%v", playlist, err)
	}
	for _, want := range []string{"#EXTM3U\n", "#EXTINF:-1,P2 分P\n", "剧.mp4\n"} {
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
	if want := "#EXTM3U\n#EXTINF:-1,P1\n剧.mp4\n"; string(body) != want {
		t.Errorf("重试后的播放列表 = %q, want %q", body, want)
	}
}
