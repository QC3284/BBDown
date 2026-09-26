package download

import (
	"bytes"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// ---- 速率/ETA 的结构性判据（可注入时钟） ----
//
// 速率与 ETA 以前拿真实墙钟做分母、断言成「等于某个数」：把 lastTime 拨回 2 秒、读 10 MiB，
// 期望 5.0 MB/s。但结算时刻的 time.Now() 比注入的起点晚多少取决于调度——慢 runner（尤其
// -race）上晚 20ms 以上，10 MiB / 2.02s = 4.9 MB/s，用例随机变红（CI 真栽过）。
//
// 现在时间源可注入（progressReader.now），窗口长度成为**输入的一部分**；判据也不是
// 「等于 5.0」：速率必须为正、必须落在「由输入推出的速率 ± 显示取整误差」内，
// ETA 必须与同一帧的「剩余量 / 速率」自洽。

// fakeClock 是注入 progressReader 的固定时钟：用例显式推进它，速率的分母由此确定。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

// now 是 progressReader.now 的实现：返回用例推进到的时刻。
func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance 推进时钟。速率只在窗口满 1 秒后结算，窗口长度就是两次 advance 之和。
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var (
	// frameSpeedPattern 抓进度帧里显示的速率（formatSpeed 的输出 + "/s"）。
	frameSpeedPattern = regexp.MustCompile("([0-9]+(?:\\.[0-9]+)?) (B|KB|MB|GB)/s")
	// frameETAPattern 抓进度帧里的 ETA（hh:mm:ss）。
	frameETAPattern = regexp.MustCompile("ETA ([0-9]{2}):([0-9]{2}):([0-9]{2})")
	// frameAmountsPattern 抓进度帧里的 x/y（"60.0/100.0 MB"）。
	frameAmountsPattern = regexp.MustCompile("([0-9]+(?:\\.[0-9]+)?)/([0-9]+(?:\\.[0-9]+)?) (B|KB|MB|GB)")
)

// byteScales 把进度帧里的单位换算成字节。
var byteScales = map[string]float64{"B": 1, "KB": 1024, "MB": 1024 * 1024, "GB": 1024 * 1024 * 1024}

// displayedSpeedBps 解析帧里显示的速率（字节/秒）；帧里没有速率时返回 0。
func displayedSpeedBps(t *testing.T, frame string) float64 {
	t.Helper()
	m := frameSpeedPattern.FindStringSubmatch(frame)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("速率字段 %q 不是数字：%v", m[1], err)
	}
	return v * byteScales[m[2]]
}

// displayedAmountsBytes 解析帧里显示的 x/y（按单位换算成字节）。
func displayedAmountsBytes(t *testing.T, frame string) (downloaded, total float64, ok bool) {
	t.Helper()
	m := frameAmountsPattern.FindStringSubmatch(frame)
	if m == nil {
		return 0, 0, false
	}
	down, err1 := strconv.ParseFloat(m[1], 64)
	tot, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("x/y 字段 %q/%q 不是数字", m[1], m[2])
	}
	scale := byteScales[m[3]]
	return down * scale, tot * scale, true
}

// displayedETASeconds 解析帧里显示的 ETA；没有 ETA 时返回 -1。
func displayedETASeconds(t *testing.T, frame string) float64 {
	t.Helper()
	m := frameETAPattern.FindStringSubmatch(frame)
	if m == nil {
		return -1
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	s, _ := strconv.Atoi(m[3])
	return float64(h*3600 + mi*60 + s)
}

// speedDisplayTolerance 是 formatSpeed 的显示取整误差上界：MB/GB 档保留一位小数
// （±0.05 单位），KB/B 档取整（±0.5 单位）。判据区间 = 输入推出的速率 ± 这个误差。
func speedDisplayTolerance(wantBps float64) float64 {
	switch {
	case wantBps >= 1024*1024*1024:
		return 0.05 * 1024 * 1024 * 1024
	case wantBps >= 1024*1024:
		return 0.05 * 1024 * 1024
	case wantBps >= 1024:
		return 512
	default:
		return 0.5
	}
}

// requireReportedRate 断言帧里的速率结构正确：为正，且落在「由输入推出的速率」±
// 显示取整误差的区间内。wantBps 必须完全由用例的输入（字节增量、窗口长度）算出，
// 既不能从帧里反推，也不能来自墙钟。
func requireReportedRate(t *testing.T, frame string, wantBps float64, what string) {
	t.Helper()
	got := displayedSpeedBps(t, frame)
	if got <= 0 {
		t.Fatalf("%s：帧里没有正速率（%q）：消费方据此决定是否显示速率/ETA", what, frame)
	}
	if diff := math.Abs(got - wantBps); diff > speedDisplayTolerance(wantBps) {
		t.Errorf("%s：显示速率 %v B/s 与输入推出的 %v B/s 差 %v，超过显示取整误差 %v：%q",
			what, got, wantBps, diff, speedDisplayTolerance(wantBps), frame)
	}
}

// requireETAConsistent 断言 ETA 与**同一帧**里的速率、剩余量自洽（剩余 = 总量 - 已下载，
// 三者都从帧里解析）。两个字段打架时（比如 ETA 用整份长、x/y 却用本次长度）这里就红。
func requireETAConsistent(t *testing.T, frame string) {
	t.Helper()
	rate := displayedSpeedBps(t, frame)
	down, tot, ok := displayedAmountsBytes(t, frame)
	eta := displayedETASeconds(t, frame)
	if rate <= 0 || !ok || eta < 0 {
		t.Fatalf("帧里缺少速率/x-y/ETA，无法做自洽检查：%q", frame)
	}
	remaining := tot - down
	if remaining < 0 {
		t.Fatalf("帧里的 x/y 自相矛盾（已下载 > 总量）：%q", frame)
	}
	if want := remaining / rate; math.Abs(eta-want) > 1.0 {
		t.Errorf("ETA %v 秒与同一帧的速率 %v B/s、剩余 %v 字节不自洽（期望约 %.1fs）：%q",
			eta, rate, remaining, want, frame)
	}
}

// assertWindowRate 断言速率 = 本窗口增量 / 本窗口秒数。1 B/s 的容差只吸收浮点尾数，
// 口径错误（累计、含 base、去掉 elapsed）都会远超它。
func assertWindowRate(t *testing.T, what string, got, windowBytes, windowSeconds float64) {
	t.Helper()
	if want := windowBytes / windowSeconds; math.Abs(got-want) > 1 {
		t.Errorf("%s：速率 %v B/s，期望 %v B/s（本窗口 %v 字节 / %vs）", what, got, want, windowBytes, windowSeconds)
	}
}

// ---- 接线：真实渲染循环把总量与 ETA 画进帧里 ----

// 速率由 ≥1s 的窗口结算；用一个**固定时钟**把「上一窗口起点」放在 2 秒前：
// 10 MiB / 2s = 5 MiB/s，剩余 90 MiB → ETA 18s。窗口长度来自注入的时钟，
// 与机器快慢无关（改前用 time.Now() 做分母，-race 下这里算成 4.9 MB/s → 随机红）。
//
// 判据分两层：
//   - 结构性：速率必须为正、且落在「本次读入 10 MiB / 2s」±显示取整误差内；
//     ETA 与同一帧的速率、剩余量自洽；
//   - 格式（注入时钟后是确定值）：整段信息帧逐字相等，继续钉住列宽与省略规则。
//
// 变异验证：把 progressSnapshot 的速率算式改成恒 0（或把 delta 换成含 base 的累计），
// 结构性判据变红；去掉 renderProgressInfo 的 ETA 分支，格式判据变红。
func TestProgressReaderShowsTotalAndETA(t *testing.T) {
	withFakeTerminal(t)
	// 120 列：满字段帧（bar 40 + 速率 + ETA + 总量）放得下，本用例钉住的就是这套格式。
	// 80 列终端上 ETA 会被宽度自适应裁掉（见 renderProgressFrameAt 与 progress_width_test.go）。
	withTerminalWidth(t, 120)
	const (
		mib         = 1 << 20
		transferred = 10 * mib
		window      = 2 * time.Second
		total       = 100 * mib
	)
	clock := newFakeClock()
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, transferred)),
			total:      total,
			lastTime:   clock.now().Add(-window),
			now:        clock.now,
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, transferred)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		// Close 会等渲染协程把首帧画完并擦干净（既有约定），无需 sleep。
		pr.Close()
	})

	requireReportedRate(t, out, float64(transferred)/window.Seconds(), "单线程首帧")
	requireETAConsistent(t, out)

	want := "10.00%  5.0 MB/s ETA 00:00:18 10.0/100.0 MB"
	if !strings.Contains(out, want) {
		t.Errorf("进度帧缺少 %q：%q", want, out)
	}
}

// 续传时终端进度帧的百分比 / x/y / ETA 必须统一为「含 base 的实际进度」：
// 整份 100 MiB 的文件，磁盘上已就位 50 MiB（base），本次响应声明剩余 50 MiB（total），
// 这一段再读 10 MiB——真实进度是 60%，剩余 40 MiB。
//
// 速率另外钉住口径：只算**本次传输**的 10 MiB / 2s = 5 MiB/s（base 是上一段留下的，
// 算进去会变成 30 MiB/s）。时间同样来自注入的固定时钟，不依赖墙钟。
//
// 改前这条帧报的是 current/total：10.00%、10.0/100.0 MB、ETA 00:00:18（把剩余当成
// 90 MiB，高估一倍多），而同一时刻的 --progress-json 报的是 60%——同一次续传两个数字。
// 现在两者共用 withBase/wholeTotal，口径完全一致。
//
// 变异验证：删掉 withBase 里的 pr.base → 百分比/x-y 不再含 base，本用例变红；
// 把速率算式改坏（恒 0、或把 base 卷进 delta）→ 结构性判据变红。
func TestProgressReaderResumeFrameCountsBaseBytes(t *testing.T) {
	withFakeTerminal(t)
	withTerminalWidth(t, 120) // 同上传用例：满字段帧需要 120 列
	const (
		mib         = 1 << 20
		base        = 50 * mib // 磁盘上已就位：整份 100 MiB 的一半
		transferred = 10 * mib
		window      = 2 * time.Second
	)
	clock := newFakeClock()
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, transferred)),
			total:      50 * mib, // 本次响应声明的**剩余**长度
			base:       base,
			lastTime:   clock.now().Add(-window), // 首帧即结算速率：10 MiB / 2s
			now:        clock.now,
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, transferred)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		pr.Close()
	})

	// 速率只由本次传输的 10 MiB / 2s 决定——base 不在这个区间里。
	requireReportedRate(t, out, float64(transferred)/window.Seconds(), "续传首帧的速率")
	// ETA 用帧里的「剩余量 / 速率」交叉核对：剩余 40 MiB（= base+total 减去 base+current）
	// / 5 MiB/s = 8s。改前按 90 MiB 算是 18s，这里就红。
	requireETAConsistent(t, out)

	// (base+current)/(base+total) = 60/100。
	want := "60.00%  5.0 MB/s ETA 00:00:08 60.0/100.0 MB"
	if !strings.Contains(out, want) {
		t.Errorf("续传进度帧没有按 (base+current)/(base+total) 出数：缺少 %q，实际 %q", want, out)
	}
}

// 总量未知的那条路径（分片/206 拿不到总长）不能凭空造出 x/y 或 ETA。
func TestProgressReaderWithoutTotalOmitsAmounts(t *testing.T) {
	withFakeTerminal(t)
	const (
		read   = 64
		window = 2 * time.Second
	)
	clock := newFakeClock()
	out := captureStdout(t, func() {
		pr := &progressReader{
			reader:     bytes.NewReader(make([]byte, read)),
			total:      0, // 总量未知
			lastTime:   clock.now().Add(-window),
			now:        clock.now,
			isTerminal: true,
			pacer:      newProgressPacer(),
			done:       make(chan struct{}),
			finished:   make(chan struct{}),
		}
		if _, err := pr.Read(make([]byte, read)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		pr.Close()
	})

	// 速率已知（64 B / 2s = 32 B/s）时必须是正数并显示出来。
	requireReportedRate(t, out, float64(read)/window.Seconds(), "总量未知的帧")
	// 去掉 total>0 的门槛后这两种垃圾会立刻出现：ETA 00:00:00 与 64/0 B。
	if strings.Contains(out, "ETA") {
		t.Errorf("总量未知不该显示 ETA：%q", out)
	}
	if strings.Contains(out, "/0 B") {
		t.Errorf("总量未知不该显示 x/y：%q", out)
	}
}

// progressSnapshot 的速率口径：**本窗口增量 / 本窗口实际间隔**——不含 base、不累计、
// 窗口不满 1 秒不结算。时间来自注入的固定时钟，每一步的期望值都由输入决定。
//
// 变异验证（每处都应是断言红，而非编译错）：
//   - speedBps 恒 0（速率算不出来）；
//   - delta 换成 pr.withBase(current)（把 base/历史累计算进本窗口）；
//   - 去掉 / elapsed（把增量当速率）；
//   - 去掉 elapsed >= 1.0 的门槛（每帧都结算）。
func TestProgressSnapshotSettlesRateFromWindowDelta(t *testing.T) {
	const mib = 1 << 20
	clock := newFakeClock()
	base := int64(1 * mib)
	pr := &progressReader{
		base:     base,
		lastTime: clock.now(),
		now:      clock.now,
	}

	// 第 0 帧：窗口一秒都没走、字节也没到——不能编出速率。
	pr.current = 0
	downloaded, speed := pr.progressSnapshot()
	if downloaded != base {
		t.Errorf("已完成字节 = %d，期望含 base 的 %d", downloaded, base)
	}
	if speed != 0 {
		t.Fatalf("窗口未满 1 秒就结算出速率 %v B/s，期望 0", speed)
	}

	// 半秒后到了 4 MiB：窗口仍不满 1 秒 → 速率保持未知（不按 0.5s 折算成 8 MiB/s）。
	clock.advance(500 * time.Millisecond)
	pr.current = 4 * mib
	if _, speed = pr.progressSnapshot(); speed != 0 {
		t.Fatalf("窗口只走了 0.5s 就结算出速率 %v B/s，期望 0", speed)
	}

	// 满 1 秒：本窗口增量 4 MiB / 1s = 4 MiB/s。base 是上一段传输留下的字节，
	// 卷进来会变成 5 MiB/s——这里就是判据。
	clock.advance(500 * time.Millisecond)
	downloaded, speed = pr.progressSnapshot()
	if downloaded != base+4*mib {
		t.Errorf("已完成字节 = %d，期望 %d（含 base）", downloaded, base+4*mib)
	}
	assertWindowRate(t, "首个满 1 秒的窗口", speed, 4*mib, 1.0)

	// 再走 2 秒、本窗口又到 12 MiB：速率 = 12 MiB / 2s = 6 MiB/s。
	// 累计口径（16 MiB / 3s ≈ 5.33 MiB/s）与「把增量当速率」（12 MiB/s）都在这里露馅。
	clock.advance(2 * time.Second)
	pr.current = 16 * mib
	if _, speed = pr.progressSnapshot(); math.Abs(speed-12*mib/2.0) > 1 {
		t.Errorf("第二个窗口：速率 %v B/s，期望 %v B/s（本窗口 12 MiB / 2s）", speed, float64(12*mib)/2.0)
	}
}
