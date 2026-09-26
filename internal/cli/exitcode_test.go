package cli

import (
	"context"
	"errors"
	"testing"
)

// 退出码语义（对齐上游 Console_CancelKeyPress）：成功 0、取消/中断 130、其它错误 1。
//
// 变异验证：把 exitCodeFor 里的 interruptExitCode 改成 0（旧行为）→ 本用例变红。
func TestExitCodeFor(t *testing.T) {
	if got := exitCodeFor(nil); got != 0 {
		t.Errorf("成功应当退出 0，实际 %d", got)
	}
	if got := exitCodeFor(context.Canceled); got != interruptExitCode {
		t.Errorf("取消应当退出 %d（128+SIGINT），实际 %d", interruptExitCode, got)
	}
	if got := exitCodeFor(errInterrupted); got != interruptExitCode {
		t.Errorf("中断标记应当退出 %d，实际 %d", interruptExitCode, got)
	}
	if got := exitCodeFor(errors.New("boom")); got != 1 {
		t.Errorf("普通错误应当退出 1，实际 %d", got)
	}
}
