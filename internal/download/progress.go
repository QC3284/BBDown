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
	reader     io.Reader
	total      int64
	current    int64
	lastBytes  int64
	lastTime   time.Time
	speed      string
	started    int32
	done       chan struct{}
	finished   chan struct{}
	closeOnce  sync.Once
	isTerminal bool
}

func newProgressReader(r io.Reader, total int64) *progressReader {
	pr := &progressReader{
		reader:     r,
		total:      total,
		lastTime:   time.Now(),
		isTerminal: term.IsTerminal(int(os.Stdout.Fd())),
		done:       make(chan struct{}),
		finished:   make(chan struct{}),
	}
	return pr
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		atomic.AddInt64(&pr.current, int64(n))
		if pr.isTerminal && atomic.CompareAndSwapInt32(&pr.started, 0, 1) {
			go pr.renderLoop()
		}
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
	if !pr.isTerminal {
		return
	}
	line := newProgressLine()
	ticker := time.NewTicker(125 * time.Millisecond)
	defer ticker.Stop()
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

	// 立即绘制首帧：下载若在首个 tick(125ms) 前完成，进度条也不至于完全不可见。
	render()
	for {
		select {
		case <-pr.done:
			// 收尾擦除整行（上游 Dispose 语义），把这一行还给紧随其后的日志。
			line.clear()
			return
		case <-ticker.C:
			render()
		}
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
