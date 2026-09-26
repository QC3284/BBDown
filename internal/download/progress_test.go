package download

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// ---- 纯函数：信息段格式 ----

// 数值齐全时的一帧：百分比 · 速率 · ETA · x/y，且各列固定宽度。
//
// 百分比不再由调用方传入，而是 renderProgressInfo 从 downloaded/total 推导——曾经
// 调用方可以传一个与 x/y 不一致的 pct（续传时正是如此），本用例的期望值现在由同一对
// 数字决定，那种打架在类型上就不可能再出现。
//
// 变异验证：去掉 renderProgressInfo 里 " ETA ..." 那一段 → 与 want 不等，本用例变红。
func TestRenderProgressInfoFullFrame(t *testing.T) {
	const mib = 1 << 20
	got := renderProgressInfo(progressFrame{
		speedBps:   1 * mib,
		downloaded: 25 * mib,
		total:      100 * mib,
	})
	want := " 25.00%  1.0 MB/s ETA 00:01:15 25.0/100.0 MB"
	if got != want {
		t.Errorf("帧格式不符：\n got %q\nwant %q", got, want)
	}
}

// 速率未知（下载头一秒、或整段没有数据）就不显示速率与 ETA——没有速率算不出剩余时间；
// 总量未知（拿不到 Content-Length）就不显示 x/y。两者都不假装知道。
//
// 用例更新（口径统一）：此前这几条给 progressFrame 直接塞 pct（0.25 / 0.5），而
// downloaded/total 是另一套数——「总量未知却报 25.00%」「x/y 是 0.0/100.0 却报 50.00%」
// 正是续传时那两个数字打架的同一个来源。现在百分比只由 downloaded/total 推导，
// 总量未知时它只能是 0.00%，与省略 x/y 的决定保持一致。
func TestRenderProgressInfoOmitsUnknownFields(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name string
		in   progressFrame
		want string
	}{
		{"总量已知但速率未知", progressFrame{downloaded: 25 * mib, total: 100 * mib}, " 25.00% 25.0/100.0 MB"},
		{"速率已知但总量未知", progressFrame{speedBps: mib, downloaded: 25 * mib}, "  0.00%  1.0 MB/s"},
		{"两者都未知", progressFrame{}, "  0.00%"},
		{"零速不显示速率", progressFrame{downloaded: 0, total: 100 * mib}, "  0.00% 0.0/100.0 MB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderProgressInfo(tc.in); got != tc.want {
				t.Errorf("= %q，期望 %q", got, tc.want)
			}
		})
	}
}

// 原地重绘的进度行最怕列抖动：速率与已下载量的位数变化时，ETA 与 x/y 的起点必须不动。
func TestRenderProgressInfoKeepsColumnsStable(t *testing.T) {
	const mib = 1 << 20
	slow := renderProgressInfo(progressFrame{speedBps: 1.0 * mib, downloaded: 50 * mib, total: 100 * mib})
	fast := renderProgressInfo(progressFrame{speedBps: 10.5 * mib, downloaded: 50 * mib, total: 100 * mib})
	if i, j := strings.Index(slow, "ETA "), strings.Index(fast, "ETA "); i < 0 || i != j {
		t.Errorf("ETA 列不固定：%q -> %d，%q -> %d", slow, i, fast, j)
	}
	if i, j := strings.Index(slow, "50.0/100.0 MB"), strings.Index(fast, "50.0/100.0 MB"); i < 0 || i != j {
		t.Errorf("x/y 列不固定：%q -> %d，%q -> %d", slow, i, fast, j)
	}
}

// ETA 的边界：秒数四舍五入；负值/NaN（末帧越过总长、或速率异常）夹到 00:00:00 而不是打印垃圾。
func TestFormatETA(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{0, "00:00:00"},
		{-3, "00:00:00"},
		{20.6, "00:00:21"},
		{3599, "00:59:59"},
		{3600, "01:00:00"},
		{86399, "23:59:59"},
	}
	for _, tc := range cases {
		if got := formatETA(tc.seconds); got != tc.want {
			t.Errorf("formatETA(%v) = %q，期望 %q", tc.seconds, got, tc.want)
		}
	}
	// 末帧瞬时越过总长（分片计数先加后校验）不能让 ETA 变成负数。
	got := renderProgressInfo(progressFrame{speedBps: 1 << 20, downloaded: 120 << 20, total: 100 << 20})
	if !strings.Contains(got, "ETA 00:00:00") {
		t.Errorf("越过总长时 ETA 应夹到 0：%q", got)
	}
}

// BenchmarkProgressInfoFrame 量新增信息段的单帧成本（纯格式化，不含终端写入）。
// O3 的结论是整帧 2.4µs、62.5 帧/s 上限下 ≈0.015% CPU；信息段本身只多一次除法和几次小段拼接。
func BenchmarkProgressInfoFrame(b *testing.B) {
	in := progressFrame{speedBps: 1.2 * 1024 * 1024, downloaded: 25 << 20, total: 50 << 20}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = renderProgressInfo(in)
	}
}

// ---- 接线：真实渲染循环把总量与 ETA 画进帧里 ----

// 速率由 ≥1s 的窗口结算；这里把 lastTime 拨回 2 秒，让首帧就带上速率与 ETA。
// 10 MiB / 2s = 5 MiB/s，剩余 90 MiB → ETA 18s。
//
// 变异验证：去掉 renderProgressInfo 的 ETA 分支（或把 renderProgressInfo 调用换回旧格式串），
// 输出里不再有 "10.00%  5.0 MB/s ETA 00:00:18 10.0/100.0 MB"，本用例变红。
func TestProgressReaderShowsTotalAndETA(t *testing.T) {
	withFakeTerminal(t)
	const mib = 1 << 20
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, 10*mib)),
			total:      100 * mib,
			lastTime:   time.Now().Add(-2 * time.Second),
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, 10*mib)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		// Close 会等渲染协程把首帧画完并擦干净（既有约定），无需 sleep。
		pr.Close()
	})

	want := "10.00%  5.0 MB/s ETA 00:00:18 10.0/100.0 MB"
	if !strings.Contains(out, want) {
		t.Errorf("进度帧缺少 %q：%q", want, out)
	}
}

// 续传时终端进度帧的百分比 / x/y / ETA 必须统一为「含 base 的实际进度」：
// 整份 100 MiB 的文件，磁盘上已就位 50 MiB（base），本次响应声明剩余 50 MiB（total），
// 这一段再读 10 MiB——真实进度是 60%，剩余 40 MiB。
//
// 改前这条帧报的是 current/total：10.00%、10.0/100.0 MB、ETA 00:00:18（把剩余当成
// 90 MiB，高估一倍多），而同一时刻的 --progress-json 报的是 60%——同一次续传两个数字。
// 现在两者共用 withBase/wholeTotal，口径完全一致。
//
// 变异验证：把 withBase 里的 pr.base 去掉（或 renderLoop 改回传 current），帧退回
// "10.00%  5.0 MB/s ETA 00:00:18 10.0/100.0 MB"，本用例变红。
func TestProgressReaderResumeFrameCountsBaseBytes(t *testing.T) {
	withFakeTerminal(t)
	const (
		mib  = 1 << 20
		base = 50 * mib // 磁盘上已就位：整份 100 MiB 的一半
	)
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, 10*mib)),
			total:      50 * mib, // 本次响应声明的**剩余**长度
			base:       base,
			lastTime:   time.Now().Add(-2 * time.Second), // 首帧即结算速率：10 MiB / 2s
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, 10*mib)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		pr.Close()
	})

	// (base+current)/(base+total) = 60/100；速率只看本次传输的 10 MiB / 2s = 5 MiB/s；
	// ETA 是剩余 40 MiB / 5 MiB/s = 8s（改前算的是 90 MiB → 18s）。
	want := "60.00%  5.0 MB/s ETA 00:00:08 60.0/100.0 MB"
	if !strings.Contains(out, want) {
		t.Errorf("续传进度帧没有按 (base+current)/(base+total) 出数：缺少 %q，实际 %q", want, out)
	}
}

// 总量未知的那条路径（分片/206 拿不到总长）不能凭空造出 x/y 或 ETA。
func TestProgressReaderWithoutTotalOmitsAmounts(t *testing.T) {
	withFakeTerminal(t)
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, 64)),
			total:      0, // 总量未知
			lastTime:   time.Now().Add(-2 * time.Second),
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, 64)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		pr.Close()
	})

	if !strings.Contains(out, "B/s") {
		t.Errorf("速率已知时应显示速率：%q", out)
	}
	// 去掉 total>0 的门槛后这两种垃圾会立刻出现：ETA 00:00:00 与 64/0 B。
	if strings.Contains(out, "ETA") {
		t.Errorf("总量未知不该显示 ETA：%q", out)
	}
	if strings.Contains(out, "/0 B") {
		t.Errorf("总量未知不该显示 x/y：%q", out)
	}
}
