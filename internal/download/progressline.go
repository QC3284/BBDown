package download

import (
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/util"
)

// progressLine 是一条「原地重绘」的终端进度行，多线程聚合与单线程逐块读取两条路径共用。
// 三处细节对齐上游 BBDown/Infrastructure/ProgressBar.cs：
//
//  1. 写入与日志共用 util.ConsoleLock（上游 ProgressBar 写控制台取的就是 Logger.ConsoleLock），
//     否则进度条重绘与日志写入会互相插入，终端上留下半行进度残影；
//  2. 每帧回到行首重绘，并把上一帧更长的部分补成空白，短帧不留下尾巴；
//  3. 收尾把整行擦掉（上游 Dispose → UpdateText(string.Empty)），而不是把最后一帧留在屏幕上
//     ——留着它，紧接着的日志就会和它挤在同一行，看起来像「两个进度」。
type progressLine struct {
	enabled bool
	lastLen int
}

func newProgressLine() *progressLine {
	return &progressLine{enabled: isTerminalOut()}
}

// draw 覆盖式重绘当前行。
func (p *progressLine) draw(text string) {
	if !p.enabled {
		return
	}
	if pad := p.lastLen - len(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	util.ConsoleLock.Lock()
	fmt.Print("\r" + text)
	util.SetProgressLineActive(true)
	util.ConsoleLock.Unlock()
	p.lastLen = len(text)
}

// clear 擦掉整行并把这一行交还给日志。
func (p *progressLine) clear() {
	if !p.enabled {
		return
	}
	if p.lastLen > 0 {
		util.ConsoleLock.Lock()
		fmt.Print("\r" + strings.Repeat(" ", p.lastLen) + "\r")
		util.SetProgressLineActive(false)
		util.ConsoleLock.Unlock()
		p.lastLen = 0
	}
}
