package download

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

const (
	progressBlocks = 40
	progressChars  = "|/-\\"
)

// progressReader wraps an io.Reader and shows a progress bar.
type progressReader struct {
	reader io.Reader
	// total 是**本次响应声明的长度**：续传的 206 里它只是剩余部分，不是整份文件——
	// 整份文件的长度是 base+total（见 wholeTotal）。
	total     int64
	current   int64
	lastBytes int64
	lastTime  time.Time
	// speedBps 是最近一个 ≥1s 窗口结算出的速率（字节/秒）；0 表示还没结算出速率。
	speedBps float64
	started  int32
	// base 是本次传输之前已就位的字节数（断点续传时 > 0）。
	//
	// 终端进度行、JSON 事件与观察者事件共用同一口径：百分比、x/y、ETA 报的都是
	// (base+current)/(base+total)，即「整份文件的实际进度」。只报这一次读了多少，
	// 续传时百分比会偏低、x/y 偏小、ETA 偏大（剩余量被算成整份文件减去本次传输量）。
	base int64
	// pacer 由数据路径（Read）打点、渲染协程消费：重绘不再等定时器（见 pacer.go）。
	pacer      progressPacer
	done       chan struct{}
	finished   chan struct{}
	closeOnce  sync.Once
	isTerminal bool
	// json 为真时走逐行 JSON 事件（--progress-json），与终端无关。
	json bool
	// now 是结算速率窗口用的时间源，nil 表示真实墙钟。用例注入固定时钟后，「窗口长度」
	// 就是输入的一部分，速率与 ETA 不再随 runner 快慢漂移——墙钟做分母时，-race / 慢
	// runner 上的「10 MiB / 2s」会被算成 4.9 MB/s，让只断言格式的用例随机变红
	// （见 progress_test.go 的 fakeClock）。
	now func() time.Time
	// observer 是上层（serve 的 SSE）挂进来的逐字节进度观察者，nil 表示没挂。
	//
	// 与终端进度帧 / JSON 事件走同一个渲染循环，所以回调天然在既有节流之后
	// （不是每条 Read 一次）。nil 时渲染路径与改前完全一致，一次回调都不会发生。
	observer func(ProgressEvent)
}

// isTerminalStdout 是 progressReader 的 TTY 判定。与 isTerminalOut 同一理由做成变量：
// 用例需要在不依赖真实终端的前提下驱动单线程进度帧（CI 的 stdout 是管道）。
var isTerminalStdout = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// newProgressReader 构造进度读取器；observer 来自 WithProgressObserver（可为 nil）。
func newProgressReader(r io.Reader, total int64, observer func(ProgressEvent)) *progressReader {
	pr := &progressReader{
		reader:     r,
		total:      total,
		lastTime:   time.Now(),
		isTerminal: isTerminalStdout(),
		json:       progressJSONEnabled.Load(),
		observer:   observer,
		pacer:      newProgressPacer(),
		done:       make(chan struct{}),
		finished:   make(chan struct{}),
	}
	return pr
}

// withBase 把「本次传输已写入的字节」换算成整份文件的已完成字节（续传时加上磁盘上
// 已就位的 base）。进度行的百分比 / x/y / ETA 只能从这里的口径出数。
func (pr *progressReader) withBase(current int64) int64 { return pr.base + current }

// wholeTotal 返回整份文件的总长（含 base）：续传的 total 只是本次响应的剩余部分。
func (pr *progressReader) wholeTotal() int64 { return pr.base + pr.total }

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		atomic.AddInt64(&pr.current, int64(n))
		// --progress-json 与观察者不受终端限制：前者给管道里的自动化，后者给进程内的
		// 上层（serve 的 SSE）。没装观察者时这一行与改前逐字相同。
		if (pr.isTerminal || pr.json || pr.observer != nil) && atomic.CompareAndSwapInt32(&pr.started, 0, 1) {
			go pr.renderLoop()
		}
		// 数据到达即打点（合并式、不阻塞）：进度帧由数据驱动，不再等 125ms 定时器。
		pr.pacer.Signal()
	}
	return n, err
}

// Close 停掉渲染协程，并**等它把进度行擦干净**再返回。
//
// 不等的话，紧接着的日志会与最后一帧挤在同一行：上游用 using 作用域保证
// ProgressBar.Dispose 先于后续日志，这里用 finished 通道表达同一时序。
func (pr *progressReader) Close() {
	pr.closeOnce.Do(func() { close(pr.done) })
	if atomic.LoadInt32(&pr.started) == 1 {
		<-pr.finished
	}
}

func (pr *progressReader) renderLoop() {
	defer close(pr.finished)
	if pr.json {
		pr.renderJSONLoop()
		return
	}
	if !pr.isTerminal {
		// 非终端（serve / 管道）且没有 --progress-json 时，改前直接返回、什么都不画。
		// 只有挂了观察者才需要这条纯回调循环（serve 的 stdout 不是终端）。
		if pr.observer != nil {
			pr.renderObserverLoop()
		}
		return
	}
	line := newProgressLine()
	animIdx := 0

	render := func() {
		// 出数走 withBase/wholeTotal：续传时百分比、x/y、ETA、观察者事件同口径。
		downloaded, speedBps := pr.progressSnapshot()

		// 动画字符紧跟在进度条后面，其后的百分比/速率/ETA/总量各占固定列宽，
		// 数字位数变化时后面的列不会左右抖动（进度行在终端里是原地重绘的）。
		line.draw(renderProgressFrame(downloaded, pr.wholeTotal(), speedBps,
			progressChars[animIdx%len(progressChars)]))
		animIdx++
		// 观察者在同一帧里、画完之后被调用（不持锁：帧数字已复制成值）。
		pr.notifyObserver(downloaded, speedBps)
	}

	// 首帧、节流、静默心跳与收尾擦行统一在 runProgressLoop 里（见 pacer.go）：
	// 上游此处是 125ms 定时器，本仓按「数据到达即重绘」的节奏驱动。
	runProgressLoop(pr.pacer.Signals(), pr.done, true, line, render)
	pr.notifyObserverFinal()
}

// progressSnapshot 取一帧的「整份文件已完成字节 / 速率」并推进 1 秒结算窗口。
//
// 终端进度行与观察者共用它，所以两处数字必然一致。速率口径与 --progress-json 的
// emitter 一致（增量 / 实际间隔），而不是把增量当归一化到 1 秒的值：ETA 直接由这个
// 数字推算，间隔略大于 1s（静默心跳下最多 1.125s）会被放大成多出来的十几秒。
// 增量只看**本次传输**：base 是上一段传输留下的字节，算进来会把续传第一个窗口的
// 速率放大成假值。
//
// 只由渲染协程调用（与改前一样，帧序号/速度状态是协程私有的），无需加锁。
func (pr *progressReader) progressSnapshot() (downloaded int64, speedBps float64) {
	current := atomic.LoadInt64(&pr.current)
	now := pr.nowTime()
	if elapsed := now.Sub(pr.lastTime).Seconds(); elapsed >= 1.0 {
		delta := current - pr.lastBytes
		if delta > 0 {
			pr.speedBps = float64(delta) / elapsed
		}
		pr.lastBytes = current
		pr.lastTime = now
	}
	return pr.withBase(current), pr.speedBps
}

// nowTime 返回结算时刻：默认真实墙钟；用例注入固定时钟（pr.now）后时间不再流逝，
// 速率只由「字节增量 / 注入的窗口长度」决定。
func (pr *progressReader) nowTime() time.Time {
	if pr.now != nil {
		return pr.now()
	}
	return time.Now()
}

// renderObserverLoop 是「无终端、无 --progress-json，只有观察者」时的渲染循环
// （serve 的场景）：节奏与另两条完全一致——首帧、minFrameInterval 节流、
// idleHeartbeat 静默心跳、收尾由 done 关闭，全部复用 runProgressLoop。
func (pr *progressReader) renderObserverLoop() {
	runProgressLoop(pr.pacer.Signals(), pr.done, true, &progressLine{}, func() {
		downloaded, speedBps := pr.progressSnapshot()
		pr.notifyObserver(downloaded, speedBps)
	})
	pr.notifyObserverFinal()
}

// notifyObserver 把一帧交给观察者；observer 为 nil 时立刻返回（不进回调路径）。
// 调用点不持任何锁：数字都是值参数。
func (pr *progressReader) notifyObserver(downloaded int64, speedBps float64) {
	if pr.observer == nil {
		return
	}
	pr.observer(ProgressEvent{Current: downloaded, Total: pr.wholeTotal(), SpeedBps: speedBps})
}

// notifyObserverFinal 在渲染循环结束后补发一条收尾事件：末帧可能因节流被合并掉，
// 这一条保证观察者一定看到最终计数（与 --progress-json 的 done 帧同一时序与口径）。
func (pr *progressReader) notifyObserverFinal() {
	if pr.observer == nil {
		return
	}
	current := atomic.LoadInt64(&pr.current)
	pr.observer(ProgressEvent{Current: pr.withBase(current), Total: pr.wholeTotal(), SpeedBps: pr.speedBps})
}

// renderJSONLoop 是 --progress-json 的渲染循环：与终端进度条共用同一套
// 「首帧 / 节流 / 静默心跳 / 收尾」节奏（runProgressLoop），只把帧渲染换成一行 JSON。
//
// 用零值 progressLine（enabled=false）：JSON 是给程序消费的，这一行不能再去动终端。
// 退出前补一条 state=done —— Close() 要等 finished，所以调用方拿到 Close 返回时，
// 结束事件一定已经写出去了。
func (pr *progressReader) renderJSONLoop() {
	// 与终端进度行同一个口径：整份文件的总长（含 base）与整份文件的已完成字节。
	emit := newJSONProgressEmitter(pr.wholeTotal())
	downloaded := func() int64 { return pr.withBase(atomic.LoadInt64(&pr.current)) }
	runProgressLoop(pr.pacer.Signals(), pr.done, true, &progressLine{}, func() {
		emit.progress(downloaded())
		// 观察者与 JSON 事件同帧、同速率口径（emitter 的 1 秒窗口）。
		pr.notifyObserver(downloaded(), emit.speed)
	})
	emit.done(downloaded())
	// 收尾事件与 done 帧同一时序：Close() 返回时它一定已经回调过。
	pr.notifyObserver(downloaded(), emit.speed)
}

// progressFrame 是一帧进度条「数字部分」的纯数据输入（renderProgressFrame 把它交给
// renderProgressInfo）。
//
// downloaded 与 total 都是**含 base 的实际进度**口径（base 见 progressReader.base）：
// 百分比、x/y、ETA 全部由这一对数字推导，调用方不再各自算一套——曾经的 bug 正是
// 百分比按 current/total、x/y 却按 (base+current)/total，同一条进度行里两个数字打架。
type progressFrame struct {
	speedBps   float64 // 速率（字节/秒）；<= 0 表示还没结算出速率
	downloaded int64   // 整份文件已完成的字节
	total      int64   // 整份文件总字节；<= 0 表示总量未知
}

// progressPercent 是百分比 / x/y / ETA 三者共用的比例：已完成 / 总量。
// 总量未知时为 0（不假装知道）；分片计数先加后校验的瞬时越过夹到 1。
func progressPercent(downloaded, total int64) float64 {
	if total <= 0 {
		return 0
	}
	pct := float64(downloaded) / float64(total)
	if pct > 1 {
		pct = 1
	}
	return pct
}

// renderProgressFrame 渲染整帧：进度条 + 动画字符 + 信息段。单线程读取与多线程聚合
// 两条路径共用（单一来源）——同一次下载在多线程下看到的进度行必须与单线程下逐字同格式。
func renderProgressFrame(downloaded, total int64, speedBps float64, anim byte) string {
	blocks := int(progressPercent(downloaded, total) * progressBlocks)
	bar := strings.Repeat("#", blocks) + strings.Repeat("-", progressBlocks-blocks)
	return fmt.Sprintf("                            [%s] %c%s", bar, anim, renderProgressInfo(progressFrame{
		speedBps:   speedBps,
		downloaded: downloaded,
		total:      total,
	}))
}

// renderProgressInfo 渲染进度行的信息段：
//
//	50.00%  1.2 MB/s ETA 00:03:12 25.0/50.0 MB
//
// 省略规则（用例钉住）：速率未知（下载头一秒、或整段时间没有数据）就不显示速率与 ETA——
// 没有速率算不出剩余时间；总量未知（拿不到 Content-Length 的路径）就不显示 x/y。
// 百分比用 %6.2f 右对齐、速率右对齐到固定宽度，后面的 ETA 与总量才不会随数字位数左右抖动。
func renderProgressInfo(f progressFrame) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%6.2f%%", progressPercent(f.downloaded, f.total)*100)
	if f.speedBps > 0 {
		fmt.Fprintf(&b, " %9s", formatSpeed(f.speedBps)+"/s")
		if f.total > 0 {
			remaining := f.total - f.downloaded
			if remaining < 0 {
				remaining = 0 // 末帧可能瞬时越过总长（分片计数先加后校验），夹到 0 而不是负 ETA
			}
			fmt.Fprintf(&b, " ETA %s", formatETA(float64(remaining)/f.speedBps))
		}
	}
	if f.total > 0 {
		fmt.Fprintf(&b, " %s", formatTransferAmounts(f.downloaded, f.total))
	}
	return b.String()
}

// formatETA 把剩余秒数写成 hh:mm:ss（四舍五入到秒）。
func formatETA(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	total := int64(math.Round(seconds))
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

// formatTransferAmounts 用**同一个单位**渲染「已写入/总量」（25.0/50.0 MB）：两个数同单位才可比。
// 单位由总量决定，否则 512 B 的已写入量会把总量一起带成 B 档，读起来比百分比还不如。
func formatTransferAmounts(downloaded, total int64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case total >= gib:
		return fmt.Sprintf("%.2f/%.2f GB", float64(downloaded)/gib, float64(total)/gib)
	case total >= mib:
		return fmt.Sprintf("%.1f/%.1f MB", float64(downloaded)/mib, float64(total)/mib)
	case total >= kib:
		return fmt.Sprintf("%.0f/%.0f KB", float64(downloaded)/kib, float64(total)/kib)
	default:
		return fmt.Sprintf("%d/%d B", downloaded, total)
	}
}

func formatSpeed(size float64) string {
	switch {
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MB", size/(1024*1024))
	case size >= 1024:
		return fmt.Sprintf("%.0f KB", size/1024)
	default:
		return fmt.Sprintf("%.0f B", size)
	}
}
