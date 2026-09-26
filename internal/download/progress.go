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
	reader    io.Reader
	total     int64
	current   int64
	lastBytes int64
	lastTime  time.Time
	// speedBps 是最近一个 ≥1s 窗口结算出的速率（字节/秒）；0 表示还没结算出速率。
	speedBps float64
	started  int32
	// base 是本次传输之前已就位的字节数（断点续传时 > 0）：JSON 事件报的是
	// 「文件已完成多少」而不是「这一次读了多少」，否则续传的 percent 永远到不了 100%。
	// 终端进度条不读它（行为与改前一致）。
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

func newProgressReader(r io.Reader, total int64) *progressReader {
	pr := &progressReader{
		reader:     r,
		total:      total,
		lastTime:   time.Now(),
		isTerminal: term.IsTerminal(int(os.Stdout.Fd())),
		json:       progressJSONEnabled.Load(),
		pacer:      newProgressPacer(),
		done:       make(chan struct{}),
		finished:   make(chan struct{}),
	}
	return pr
}

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
				// 1.125s）会被放大成多出来的十几秒。
				pr.speedBps = float64(delta) / elapsed
			}
			pr.lastBytes = current
			pr.lastTime = now
		}

		pct := float64(0)
		if pr.total > 0 {
			pct = float64(current) / float64(pr.total)
		}
		if pct > 1 {
			pct = 1
		}

		blocks := int(pct * progressBlocks)
		bar := strings.Repeat("#", blocks) + strings.Repeat("-", progressBlocks-blocks)
		anim := progressChars[animIdx%len(progressChars)]
		animIdx++

		// 动画字符紧跟在进度条后面，其后的百分比/速率/ETA/总量各占固定列宽，
		// 数字位数变化时后面的列不会左右抖动（进度行在终端里是原地重绘的）。
		line.draw(fmt.Sprintf("                            [%s] %c%s", bar, anim,
			renderProgressInfo(progressFrame{
				pct:        pct,
				speedBps:   pr.speedBps,
				downloaded: current,
				total:      pr.total,
			})))
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
	emit := newJSONProgressEmitter(pr.total)
	downloaded := func() int64 { return pr.base + atomic.LoadInt64(&pr.current) }
	runProgressLoop(pr.pacer.Signals(), pr.done, true, &progressLine{}, func() { emit.progress(downloaded()) })
	emit.done(downloaded())
}

// progressFrame 是一帧进度条「数字部分」的纯数据输入（渲染循环把它交给 renderProgressInfo）。
type progressFrame struct {
	pct        float64 // 已完成比例，调用方已夹到 [0,1]
	speedBps   float64 // 速率（字节/秒）；<= 0 表示还没结算出速率
	downloaded int64   // 本次传输已写入的字节
	total      int64   // 总字节；<= 0 表示总量未知
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
	fmt.Fprintf(&b, "%6.2f%%", f.pct*100)
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
