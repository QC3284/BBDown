package server

import (
	"context"
	"errors"
	"testing"
)

// 本文件搬上游 CancellationClassificationTests：取消与超时必须分开归类，
// 否则任务被误标「已取消」，真实失败原因被掩盖成用户操作。

func TestUpstreamClassifyTaskCancellation(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	status, msg := classifyTaskCancellation(cancelledCtx, errors.New("解析请求超时或被中断"))
	if status != StatusCancelled {
		t.Errorf("真取消应归为 Cancelled，实际 %v", status)
	}
	if msg != "已取消" {
		t.Errorf("真取消的文案应为「已取消」，实际 %q", msg)
	}

	status, msg = classifyTaskCancellation(context.Background(), errors.New("解析请求超时或被中断"))
	if status != StatusFailed {
		t.Errorf("未取消 token 的取消类错误应归为 Failed，实际 %v", status)
	}
	if msg != "解析请求超时或被中断" {
		t.Errorf("Failed 应保留原始错误信息，实际 %q", msg)
	}

	dlCtx, dlCancel := context.WithTimeout(context.Background(), 0)
	defer dlCancel()
	<-dlCtx.Done()
	if s, _ := classifyTaskCancellation(dlCtx, context.DeadlineExceeded); s != StatusFailed {
		t.Errorf("超时上下文应归为 Failed（不是用户取消），实际 %v", s)
	}
}
