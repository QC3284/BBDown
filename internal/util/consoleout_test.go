package util

import (
	"bytes"
	"strings"
	"testing"
)

// 机读模式的底线：stdout 只留数据。SetConsoleOutput 把日志挪到 stderr（或任意 writer），
// `--info-json` / `doctor --json` 依赖它保证管道拿到的是干净 JSON。
//
// 变异验证：把 SetConsoleOutput 改成空实现 → 本用例变红。
func TestSetConsoleOutputRoutesLogs(t *testing.T) {
	var buf bytes.Buffer
	SetConsoleOutput(&buf)
	t.Cleanup(func() { SetConsoleOutput(nil) })

	Log("挪到别的流的日志 %d", 7)
	if !strings.Contains(buf.String(), "挪到别的流的日志 7") {
		t.Errorf("日志应当写进指定流，实际缓冲区：%q", buf.String())
	}

	var second bytes.Buffer
	SetConsoleOutput(&second)
	LogWarn("第二条")
	if !strings.Contains(second.String(), "第二条") {
		t.Errorf("切换后应当写进新流，实际：%q", second.String())
	}
}
