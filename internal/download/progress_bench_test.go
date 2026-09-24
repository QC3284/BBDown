package download

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// BenchmarkProgressRenderFrame 是 O3 的数字来源：实时进度条受 minFrameInterval（16ms）节流，
// 所以它对 CPU 的上限贡献 = 62.5 帧/秒 × 单帧成本。这里量单帧：算条形、格式化、加锁、写行。
func BenchmarkProgressRenderFrame(b *testing.B) {
	origTerm := isTerminalOut
	isTerminalOut = func() bool { return true }
	defer func() { isTerminalOut = origTerm }()

	// 终端 I/O 不是被测对象：丢弃输出，只留渲染与加锁成本。
	oldStdout := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	os.Stdout = devnull
	defer func() { os.Stdout = oldStdout; devnull.Close() }()

	line := newProgressLine()
	bar := strings.Repeat("#", 20) + strings.Repeat("-", 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		line.draw(fmt.Sprintf("                            [%s] %6.2f%% %c%s", bar, 50.0, '|', " 1.2 MB/s"))
	}
}
