package download

import (
	"fmt"
	"io"
	"os"
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
	isTerminal bool
}

func newProgressReader(r io.Reader, total int64) *progressReader {
	pr := &progressReader{
		reader:     r,
		total:      total,
		lastTime:   time.Now(),
		isTerminal: term.IsTerminal(int(os.Stdout.Fd())),
		done:       make(chan struct{}),
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

func (pr *progressReader) Close() {
	close(pr.done)
}

func (pr *progressReader) renderLoop() {
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
		bar := make([]byte, progressBlocks)
		for i := 0; i < progressBlocks; i++ {
			if i < blocks {
				bar[i] = '#'
			} else {
				bar[i] = '-'
			}
		}
		anim := progressChars[animIdx%len(progressChars)]
		animIdx++

		fmt.Fprintf(os.Stdout, "                            [%s] %6.2f%% %c%s\r",
			string(bar), pct*100, anim, pr.speed)
	}

	// 立即绘制首帧：下载若在首个 tick(125ms) 前完成，进度条也不至于完全不可见。
	render()
	for {
		select {
		case <-pr.done:
			// 结束前补一帧最终状态(100%)并换行，快速下载也能看到完整进度条。
			render()
			fmt.Fprint(os.Stdout, "\n")
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
