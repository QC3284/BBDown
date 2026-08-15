package workflow

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadIntSafeSuccess(t *testing.T) {
	old := stdinReader
	defer func() { stdinReader = old }()
	stdinReader = strings.NewReader("5\n")
	v, ok := readIntSafe(context.Background())
	if !ok || v != 5 {
		t.Fatalf("readIntSafe = (%d, %v), want (5, true)", v, ok)
	}
}

func TestReadIntSafeInvalidInput(t *testing.T) {
	old := stdinReader
	defer func() { stdinReader = old }()
	stdinReader = strings.NewReader("not-a-number\n")
	v, ok := readIntSafe(context.Background())
	// 非法输入保持既有行为: 返回 0 并继续(与 Fscanf 失败时 v 保持 0 一致)。
	if !ok || v != 0 {
		t.Fatalf("readIntSafe = (%d, %v), want (0, true)", v, ok)
	}
}

func TestReadIntSafeCancelled(t *testing.T) {
	old := stdinReader
	defer func() { stdinReader = old }()
	// 阻塞型 reader 模拟用户一直不输入; 取消后必须返回 ok=false。
	stdinReader = &blockingReader{}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, ok := readIntSafe(ctx)
	if ok {
		t.Fatal("cancelled readIntSafe should report ok=false")
	}
}

// blockingReader 永远阻塞(模拟终端等待输入)。
type blockingReader struct{}

func (b *blockingReader) Read(p []byte) (int, error) {
	select {}
}

var _ io.Reader = (*blockingReader)(nil)
