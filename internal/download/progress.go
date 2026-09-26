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
	// 终端进度行与 JSON 事件共用同一口径：百分比、x/y、ETA 报的都是
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
}

// isTerminalStdout 是 progressReader 的 TTY 判定。与 isTerminalOut 同一理由做成变量：
// 用例需要在不依赖真实终端的前提下驱动单线程进度帧（CI 的 stdout 是管道）。
var isTerminalStdout = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func newProgressReader(r io.Reader, total int64) *progressReader {
	pr := &progressReader{
		reader:     r,
		total:      total,
		lastTime:   time.Now(),
		isTerminal: isTerminalStdout(),
		json:       progressJSONEnabled.Load(),
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
		// --progress-json 时不受终端限制：这正是给管道里的自动化用的。
		if (pr.isTerminal || pr.json) && atomic.CompareAndSwapInt32(&pr.started, 0, 1) {
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
		return
	}
	line := newProgressLine()
	animIdx := 0

	render := func() {
		current := atomic.LoadInt64(&pr.current)
		now := time.Now()
		elapsed := now.Sub(pr.lastTime).Seconds()
		if elapsed >= 1.0 {
			delta := current - pr.lastBytes
			if delta > 0 {
				// 速率口径与 --progress-json 的 emitter 一致（增量 / 实际间隔），而不是把增量
				// 当归一化到 1 秒的值：ETA 直接由这个数字推算，间隔略大于 1s（静默心跳下最多
				// 1.125s）会被放大成多出来的十几秒。增量只看**本次传输**：base 是上一段传输
				// 留下的字节，算进来会把续传第一个窗口的速率放大成假值。
				pr.speedBps = float64(delta) / elapsed
			}
			pr.lastBytes = current
			pr.lastTime = now
		}

		// 动画字符紧跟在进度条后面，其后的百分比/速率/ETA/总量各占固定列宽，
		// 数字位数变化时后面的列不会左右抖动（进度行在终端里是原地重绘的）。
		// 出数走 withBase/wholeTotal：续传时百分比、x/y、ETA 与 JSON 事件同口径。
		line.draw(renderProgressFrame(pr.withBase(current), pr.wholeTotal(), pr.speedBps,
			progressChars[animIdx%len(progressChars)]))
		animIdx++
	}

	// 首帧、节流、静默心跳与收尾擦行统一在 runProgressLoop 里（见 pacer.go）：
	// 上游此处是 125ms 定时器，本仓按「数据到达即重绘」的节奏驱动。
	runProgressLoop(pr.pacer.Signals(), pr.done, true, line, render)
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
	runProgressLoop(pr.pacer.Signals(), pr.done, true, &progressLine{}, func() { emit.progress(downloaded()) })
	emit.done(downloaded())
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
