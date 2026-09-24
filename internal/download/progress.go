package download

import (
	"fmt"
	"io"
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
	speed     string
	started   int32
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
				pr.speed = " " + formatSpeed(float64(delta)) + "/s"
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

		line.draw(fmt.Sprintf("                            [%s] %6.2f%% %c%s", bar, pct*100, anim, pr.speed))
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
