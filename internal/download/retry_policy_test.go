package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// 任务 C：planTrackRetry 的分类表。每个错误类别至少一条，另外钉住两条曲线
// （412 的 1s→2s→4s 上限 8s、其它错误沿用 (attempt+1)×--retry-delay）与
// 「有候选就立刻换（Backoff=0）」。
//
// 变异验证（每条都要「改回旧行为即红」）：
//   - 412 的退避改成 defaultBackoff（旧的统一阶梯）→ 412 各行变红；
//   - NeedsUARotation 去掉 → 412 各行变红；
//   - 网络类 candidates>1 不再 SwitchCandidate → 「立刻换候选」各行变红；
//   - 网络类无候选改回 defaultBackoff → 短退避各行变红；
//   - 4xx（非 412/404）改成可重试 → 403 行变红；
//   - 5xx 改成 SwitchCandidate → 5xx 行变红。
func TestPlanTrackRetryClassifiesEachErrorKind(t *testing.T) {
	const delay = 3 * time.Second // 生产默认 --retry-delay

	networkErr := &url.Error{Op: "Get", URL: "https://cdn.example/a.m4s", Err: errors.New("connection refused")}
	netOpErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	status := func(code int) error {
		return &httpStatusError{code: code, msg: fmt.Sprintf("download failed: HTTP %d", code)}
	}

	cases := []struct {
		name        string
		err         error
		attempt     int
		candidates  int
		retryDelay  time.Duration
		wantClass   retryClass
		wantRetry   bool
		wantSwitch  bool
		wantBackoff time.Duration
		wantRotate  bool
	}{
		{"没有错误就没什么可重试的", nil, 0, 1, delay, retryClassOther, false, false, 0, false},

		{"412 首次退避 1s 且要求轮换 UA", status(http.StatusPreconditionFailed), 0, 1, delay, retryClassRiskControl, true, false, time.Second, true},
		{"412 第二次退避翻倍到 2s", status(http.StatusPreconditionFailed), 1, 1, delay, retryClassRiskControl, true, false, 2 * time.Second, true},
		{"412 第三次 4s", status(http.StatusPreconditionFailed), 2, 1, delay, retryClassRiskControl, true, false, 4 * time.Second, true},
		{"412 封顶 8s", status(http.StatusPreconditionFailed), 3, 1, delay, retryClassRiskControl, true, false, 8 * time.Second, true},
		{"412 再多次也不超上限", status(http.StatusPreconditionFailed), 20, 1, delay, retryClassRiskControl, true, false, 8 * time.Second, true},

		{"404 有候选：立刻换地址、不退避", status(http.StatusNotFound), 1, 3, delay, retryClassNotFound, true, true, 0, false},
		{"404 无候选：沿用既有阶梯（上游 3×3=9 次的阶梯不动）", status(http.StatusNotFound), 0, 1, delay, retryClassNotFound, true, false, 3 * time.Second, false},
		{"404 无候选第二次：2×delay", status(http.StatusNotFound), 1, 1, delay, retryClassNotFound, true, false, 6 * time.Second, false},

		{"5xx 不换候选、沿用既有退避", status(http.StatusInternalServerError), 0, 2, delay, retryClassServer, true, false, 3 * time.Second, false},
		{"5xx 第二次 2×delay", status(http.StatusBadGateway), 1, 1, delay, retryClassServer, true, false, 6 * time.Second, false},

		{"4xx（非 412/404）确定性失败不重试", status(http.StatusForbidden), 0, 3, delay, retryClassFatal, false, false, 0, false},

		{"网络错误有候选：立刻换地址、不退避", networkErr, 0, 2, delay, retryClassNetwork, true, true, 0, false},
		{"net.Error 同样归网络类", netOpErr, 0, 2, delay, retryClassNetwork, true, true, 0, false},
		{"传输中断（io.ErrUnexpectedEOF）同样归网络类", io.ErrUnexpectedEOF, 0, 3, delay, retryClassNetwork, true, true, 0, false},
		{"网络错误无候选：短退避 500ms", networkErr, 2, 1, delay, retryClassNetwork, true, false, 500 * time.Millisecond, false},
		{"用户把 --retry-delay 设得更短时按用户的来", networkErr, 0, 1, 100 * time.Millisecond, retryClassNetwork, true, false, 100 * time.Millisecond, false},

		{"Range 不支持：交给调用方降级单线程，不重试", ErrRangeNotSupported, 0, 3, delay, retryClassFatal, false, false, 0, false},
		{"ctx 取消：不重试、不换候选", context.Canceled, 0, 3, delay, retryClassFatal, false, false, 0, false},
		{"包在 *url.Error 里的超时同样不重试", &url.Error{Op: "Get", URL: "https://cdn.example/a", Err: context.DeadlineExceeded}, 0, 3, delay, retryClassFatal, false, false, 0, false},

		{"本地错误（产物长度不符）：沿用既有阶梯", errors.New("下载产物长度(1)与服务器声明(2)不符"), 1, 1, delay, retryClassOther, true, false, 6 * time.Second, false},
		{"--retry-delay 为 0：仍旧重试但不等待", status(http.StatusServiceUnavailable), 0, 1, 0, retryClassServer, true, false, 0, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planTrackRetry(c.err, c.attempt, c.candidates, c.retryDelay)
			if got.Class != c.wantClass {
				t.Errorf("类别 = %v，期望 %v", got.Class, c.wantClass)
			}
			if got.Retry != c.wantRetry {
				t.Errorf("Retry = %v，期望 %v", got.Retry, c.wantRetry)
			}
			if got.SwitchCandidate != c.wantSwitch {
				t.Errorf("SwitchCandidate = %v，期望 %v", got.SwitchCandidate, c.wantSwitch)
			}
			if got.Backoff != c.wantBackoff {
				t.Errorf("Backoff = %v，期望 %v", got.Backoff, c.wantBackoff)
			}
			if got.NeedsUARotation != c.wantRotate {
				t.Errorf("NeedsUARotation = %v，期望 %v", got.NeedsUARotation, c.wantRotate)
			}
			if got.SwitchCandidate && got.Backoff != 0 {
				t.Errorf("换候选时必须立刻重试（Backoff=0），实际 %v", got.Backoff)
			}
		})
	}
}

// 412 的退避曲线由 util.RiskControlBackoff 提供，这里把下载层实际拿到的数值再钉一遍：
// 基线 1s、翻倍、上限 8s——三个数缺一不可（少了上限会让 --retry-count 很大时退避炸掉）。
func TestPlanTrackRetryRiskControlBackoffIsCapped(t *testing.T) {
	for attempt, want := range map[int]time.Duration{
		0: time.Second, 1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 100: 8 * time.Second,
	} {
		got := planTrackRetry(&httpStatusError{code: http.StatusPreconditionFailed}, attempt, 1, 3*time.Second)
		if got.Backoff != want {
			t.Errorf("412 第 %d 次失败后的退避 = %v，期望 %v", attempt+1, got.Backoff, want)
		}
	}
}
