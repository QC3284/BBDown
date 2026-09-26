package download

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// 任务 C：按错误分类的重试策略（替换「一套阶梯打天下」）。
//
// 此前 412 风控、网络/传输错误、5xx 与 404 走同一套退避：都由 attempt×--retry-delay 决定等多久，
// 都由 candidateAdvanceable 决定换不换候选。分类之后，「某个错误 → 退避时长 / 是否换候选 /
// 是否重试」只在 planTrackRetry 一处决定（单线程与多线程分片两条循环共用）。
//
// planTrackRetry 是纯函数：不读时钟、不做 IO、不写全局状态；退避曲线的参数取自包变量，仅供
// 用例把等待收窄到毫秒（与 downloadStallTimeout 同一套做法）。
type retryClass int

const (
	// retryClassOther：本地 IO、产物长度不符、未知错误——沿用配置退避重试
	// （上游把 IOException 一并重试，重试一次不会更糟）。
	retryClassOther retryClass = iota
	// retryClassFatal：确定性失败，重试同一个地址只会拿到同样结果——4xx（412/404 除外）、
	// Range 不支持、ctx 取消。
	retryClassFatal
	// retryClassRiskControl：HTTP 412——B 站风控信号（不是参数错误）。
	retryClassRiskControl
	// retryClassNotFound：HTTP 404——镜像没覆盖该对象，候选链的触发条件。
	retryClassNotFound
	// retryClassServer：HTTP 5xx——上游临时故障，换候选没用。
	retryClassServer
	// retryClassNetwork：*url.Error / net.Error / io.ErrUnexpectedEOF 等连接与传输失败。
	retryClassNetwork
)

func (c retryClass) String() string {
	switch c {
	case retryClassFatal:
		return "确定性失败"
	case retryClassRiskControl:
		return "412风控"
	case retryClassNotFound:
		return "404"
	case retryClassServer:
		return "5xx"
	case retryClassNetwork:
		return "网络/传输失败"
	default:
		return "其它错误"
	}
}

// retryPlan 是一次失败之后的完整决策。
type retryPlan struct {
	Class retryClass
	// Retry=false 表示确定性失败，调用方应立即带着原错误返回。
	Retry bool
	// SwitchCandidate=true 表示这次失败说明「当前地址不行」，且链上还有下一个候选：
	// 调用方应前进一格并**立刻**重试（Backoff=0）。没有候选时该字段恒为 false。
	SwitchCandidate bool
	// Backoff 是下一次尝试前的退避时长（0 表示不等）。
	Backoff time.Duration
	// NeedsUARotation=true（仅 412）表示调用方必须**先成功轮换自动 UA**才允许重试；
	// 轮换失败（显式 --user-agent）时应放弃重试。
	NeedsUARotation bool
}

// 412 的退避曲线：首次 1s、之后翻倍、上限 8s。与 internal/util 的 412 策略共用同一条曲线
// （util.RiskControlBackoff），避免两层各自漂移；变量化是为了让用例把等待缩到毫秒。
var (
	riskControlBackoffBase = time.Second
	riskControlBackoffMax  = 8 * time.Second
	// networkRetryBackoff 是「没有候选可换」时的网络短退避上限：网络抖动重试代价小、收益高，
	// 不该等满 --retry-delay；用户把 --retry-delay 设得更短时按用户的来（见 shortNetworkBackoff）。
	networkRetryBackoff = 500 * time.Millisecond
)

// defaultBackoff 是 5xx 与其它错误沿用的既有退避：上游 BBDownDownloadUtil 的
// (retry+1)×RetryDelayMs（retry 为已失败次数）。本次只做分类，没有调整这条曲线的节奏。
func defaultBackoff(attempt int, retryDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if retryDelay < 0 {
		retryDelay = 0
	}
	return time.Duration(attempt+1) * retryDelay
}

// shortNetworkBackoff 取「用户的 --retry-delay」与「网络短退避上限」里更小的那个：
// 既保证无候选时的网络重试足够快，又不会把用户显式调短的等待又拉长回去。
func shortNetworkBackoff(retryDelay time.Duration) time.Duration {
	if retryDelay <= 0 {
		return 0
	}
	if retryDelay < networkRetryBackoff {
		return retryDelay
	}
	return networkRetryBackoff
}

// classifyTrackError 把错误分到「应对方式」上等价的那一类。
//
// ctx 取消必须排在 *url.Error 之前判断：client.Do 会把 context.Canceled 包进 *url.Error，
// 而取消不是「地址不行」，不该换候选、也不该重试。
func classifyTrackError(err error) retryClass {
	if err == nil {
		return retryClassOther
	}
	if errors.Is(err, ErrRangeNotSupported) {
		return retryClassFatal
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return retryClassFatal
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		switch {
		case se.code == http.StatusPreconditionFailed:
			return retryClassRiskControl
		case se.code == http.StatusNotFound:
			return retryClassNotFound
		case se.code >= 400 && se.code <= 499:
			return retryClassFatal
		case se.code >= 500:
			return retryClassServer
		default:
			return retryClassOther
		}
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return retryClassNetwork
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return retryClassNetwork
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, http.ErrBodyReadAfterClose) {
		return retryClassNetwork
	}
	return retryClassOther
}

// planTrackRetry 是下载层唯一的重试决策点（纯函数）。
//
//	err        —— 刚失败的尝试返回的错误
//	attempt    —— 已失败的尝试序号（0 起），用于计算退避
//	candidates —— 候选链上「含当前地址在内」还剩几个地址（1 = 没有备份候选）
//	retryDelay —— --retry-delay 基数（5xx/其它错误与上游 (retry+1)*delay 口径一致）
//
// 分类规则（表驱动用例见 retry_policy_test.go，每类错误一条）：
//
//	412（风控）  —— 退避 1s→2s→4s… 上限 8s，且必须先成功轮换 UA 才重试（NeedsUARotation）
//	网络/传输    —— 有候选：立刻换（Backoff=0）；没有：短退避（≤500ms）
//	404          —— 有候选：立刻换；没有：沿用既有阶梯退避（无候选时与上游一致，9 次阶梯不动）
//	5xx          —— 沿用既有阶梯退避，不换候选（换地址只会拿到同样的应答）
//	4xx（非 412/404）—— 确定性失败，不重试（上游 API 层语义；换地址/重试都是白烧预算）
//	其它（本地 IO / 产物长度不符）—— 沿用既有阶梯退避
func planTrackRetry(err error, attempt, candidates int, retryDelay time.Duration) retryPlan {
	if err == nil {
		return retryPlan{Class: retryClassOther}
	}
	class := classifyTrackError(err)

	switch class {
	case retryClassFatal:
		return retryPlan{Class: class}

	case retryClassRiskControl:
		return retryPlan{
			Class:           class,
			Retry:           true,
			NeedsUARotation: true,
			Backoff:         util.RiskControlBackoff(attempt+1, riskControlBackoffBase, riskControlBackoffMax),
		}

	case retryClassNotFound, retryClassNetwork:
		if candidates > 1 {
			// 这个地址不行、还有备份：立刻换过去，不为一个死地址退避。
			return retryPlan{Class: class, Retry: true, SwitchCandidate: true}
		}
		backoff := defaultBackoff(attempt, retryDelay)
		if class == retryClassNetwork {
			backoff = shortNetworkBackoff(retryDelay)
		}
		return retryPlan{Class: class, Retry: true, Backoff: backoff}

	case retryClassServer:
		return retryPlan{Class: class, Retry: true, Backoff: defaultBackoff(attempt, retryDelay)}

	default: // retryClassOther
		return retryPlan{Class: class, Retry: true, Backoff: defaultBackoff(attempt, retryDelay)}
	}
}

// rotateUserAgentFor412 在 412 风控时轮换自动 UA，返回是否真的换了。
//
// 显式 UA 有两处：--user-agent 落在 HTTPClient 上（SetUserAgent 标记 uaExplicit），以及
// 每条下载流自己的 UserAgent 字段。后者优先级更高（DownloadConfig.userAgent），轮换
// HTTPClient 上的 UA 根本影响不到实际请求，所以这里一并按「不可轮换」处理。
func (c DownloadConfig) rotateUserAgentFor412() bool {
	if c.UserAgent != "" || c.Client == nil {
		return false
	}
	return c.Client.RotateAutomaticUserAgent()
}
