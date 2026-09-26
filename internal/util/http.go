package util

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// apiRetries and apiRetryBackoff bound the API retry policy; the backoff doubles
// per attempt. Both are variables so tests can shrink them.
var (
	apiRetries      = 3
	apiRetryBackoff = 500 * time.Millisecond
)

// 风控（HTTP 412）的重试策略：共 3 次尝试，退避按 1s → 2s → 4s … 翻倍、上限 8s，
// 且**只在成功轮换自动 UA 之后**才重试。
//
// 依据：上游把 4xx 一律视为确定性错误直接抛出；但 412 是 B 站的**限流/风控**信号（不是参数错误），
// 等在原地重试同一 UA 往往还是 412，而换一个自动 UA 立刻就能过（同一生态的 C# 2.x 接手线 BBDownT
// 2.1.x 就是这么做的：移除易触发 412 的默认 UA + 412 时轮换 UA 重试）。本仓的有意偏离，登记见
// docs/UPSTREAM_ALIGNMENT.md §4.40。变量化是为了让用例把退避缩到毫秒。
//
// 「只在轮换成功后重试」是任务 C 的收敛：显式 --user-agent 换不掉 UA，带着同一个 UA 连打正是
// 风控提示里劝阻的行为——直接抛出 412（附带下面的可操作提示），不再空转。
var (
	riskControlMaxAttempts = 3
	riskControlBackoffBase = time.Second
	riskControlBackoffMax  = 8 * time.Second
)

// RiskControlBackoff 计算 412 风控第 retry 次重试（1 起，0 按第 1 次算）前的退避：
// base 起每次翻倍，上限 max（max<=0 表示不限；base<=0 表示不退避）。
//
// 纯函数：API 层（本文件）与下载层（internal/download 的 planTrackRetry）共用同一条曲线，
// 避免两处 412 退避各自漂移。
func RiskControlBackoff(retry int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	if retry < 1 {
		retry = 1
	}
	d := base
	for i := 1; i < retry && (max <= 0 || d < max); i++ {
		d *= 2
	}
	if max > 0 && d > max {
		d = max
	}
	return d
}

// SetRetries overrides the retry count for this client (from --retry-count).
func (c *HTTPClient) SetRetries(n int) {
	if n > 0 {
		c.retries = n
	}
}

// maxResponseBodyBytes bounds an API response body (upstream 64MB). A broken
// endpoint or an --insecure MITM can otherwise stream a chunked body without
// limit and exhaust memory.
const maxResponseBodyBytes = 64 << 20

// ReadAllBounded reads r up to limit bytes and fails when the body is larger,
// instead of silently truncating it.
func ReadAllBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("响应体超过 %d 字节上限", limit)
	}
	return data, nil
}

// IsTrustedCookieHost reports whether host may receive BBDown credentials.
// A credential-bearing request must never be redirected outside this set
// (upstream IsTrustedCookieHost, RF-13/RF-37/RF-50).
func IsTrustedCookieHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	// 与上游 HTTPUtil.OfficialHostSuffixes 逐字一致（9 项）。此前漏了 biliapi.com：
	// 该域是官方 API 镜像，凭据校验会误判为「非可信主机」而拒绝发 Cookie、重定向守卫也会误拦。
	for _, suffix := range []string{"bilibili.com", "b23.tv", "bilivideo.com", "hdslb.com", "biliapi.net", "biliapi.com", "bilibili.tv", "biliintl.com", "aisee.tv"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// credentialRedirectGuard stops a credential-bearing request from following a
// redirect to a host that is not allowed to see the Cookie / access_token.
// Without it http.Client transparently forwards the request (Go only strips
// Cookie across *domains*, not across a same-domain different-port hop), so a
// 3xx from a compromised or --insecure endpoint could exfiltrate SESSDATA.
func credentialRedirectGuard(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("重定向跳数超过 10 次")
	}
	host := strings.ToLower(req.URL.Hostname())
	if !IsTrustedCookieHost(host) {
		return fmt.Errorf("拒绝跟随重定向到不可信主机 %s（该请求携带凭据）", host)
	}
	return nil
}

// HTTPClient wraps the standard http.Client with BBDown-specific behavior.
type HTTPClient struct {
	client    *http.Client
	userAgent string
	// uaMu/uaExplicit 保护 UA 的读写：轮换会发生在并发请求中，且显式 --user-agent
	// 必须**永不**被自动轮换覆盖。
	uaMu       sync.RWMutex
	uaExplicit bool
	debugFn    func(string, ...interface{})
	skipSSL    func() bool
	cookieFn   func() string

	// credentialHosts are user-opted-in hosts that may receive cookies in
	// addition to the official Bilibili domains.
	credentialHosts []string
	// retries overrides apiRetries when non-zero.
	retries int
}

// NewHTTPClient creates a new HTTPClient.
func NewHTTPClient(skipSSL func() bool, cookieFn func() string, debugFn func(string, ...interface{})) *HTTPClient {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false, // will be overridden per-request via callback
		},
		MaxIdleConns:       100,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: false,
	}

	c := &HTTPClient{
		client: &http.Client{
			Transport:     transport,
			Timeout:       2 * time.Minute,
			CheckRedirect: credentialRedirectGuard,
		},
		userAgent: randomUserAgent(),
		debugFn:   debugFn,
		skipSSL:   skipSSL,
		cookieFn:  cookieFn,
	}

	// Override TLS config to support runtime skipSSL toggle
	transport.TLSClientConfig.InsecureSkipVerify = skipSSL != nil && skipSSL()

	return c
}

var platforms = []string{
	"Windows NT 10.0; Win64",
	"Macintosh; Intel Mac OS X 10_15",
	"X11; Linux x86_64",
}

func randomVersion(min, max float64) string {
	v := min + rand.Float64()*(max-min)
	return fmt.Sprintf("%.3f", v)
}

func randomUserAgent() string {
	browsers := []string{
		// A current major version: an ancient one is itself a fingerprint, and some
		// endpoints treat an outdated UA with suspicion.
		fmt.Sprintf("AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", randomVersion(130, 150)),
		fmt.Sprintf("Gecko/20100101 Firefox/%s", randomVersion(130, 150)),
	}
	platform := platforms[rand.Intn(len(platforms))]
	return fmt.Sprintf("Mozilla/5.0 (%s) %s", platform, browsers[rand.Intn(len(browsers))])
}

// SetCredentialHosts records the hosts the user explicitly opted into with
// --host/--ep-host/--tv-host; they may receive credentials like the official
// domains do.
func (c *HTTPClient) SetCredentialHosts(hosts ...string) {
	c.credentialHosts = hosts
}

// normalizeCredentialHost reduces a configured host (which may carry a scheme
// and a port) to a bare lowercase hostname.
func normalizeCredentialHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// maySendCredentials reports whether rawURL may receive the user's cookies: an
// official Bilibili host, or one explicitly configured through
// --host/--ep-host/--tv-host. Everything else is refused — a URL assembled from
// server-controlled data (a base_url, a redirect target) could otherwise carry
// SESSDATA off to an arbitrary host.
func (c *HTTPClient) maySendCredentials(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if IsTrustedCookieHost(u.Hostname()) {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range c.credentialHosts {
		if normalizeCredentialHost(h) == host {
			return true
		}
	}
	return false
}

// GetWebSource fetches the content from a URL as a string.
// statusSuffix 为风控态补一句可操作提示。
//
// 上游此处是 .NET 的 EnsureSuccessStatusCode()，消息形如
// "Response status code does not indicate success: 412 (Precondition Failed)."——
// 用户看到 HTTP 412 不知道该等还是该换网络。本仓有意补一句（差异登记见
// docs/UPSTREAM_ALIGNMENT.md §4.33）。只对 412 生效：它是 B 站风控的固定状态码，
// 其余 4xx（参数/鉴权）照旧原样抛出，不误导。
func statusSuffix(code int) string {
	if code == http.StatusPreconditionFailed {
		return "（疑似风控拦截：请等待数分钟至数十分钟后重试，或更换网络出口；持续重试会加重风控）"
	}
	return ""
}

// StatusHint 把风控态提示（statusSuffix）导出给下载层复用：412 的中文文案只有这一份，
// 两层共享同一条「哪些状态码该提示」的规则——下载层的最终错误此前只有裸状态码，
// 用户不知道该等还是该换网络。其余状态码返回空串，调用方可无条件拼接。
func StatusHint(code int) string { return statusSuffix(code) }

func (c *HTTPClient) GetWebSource(ctx context.Context, url string) (string, error) {
	body, _, err := c.GetWebSourceWithSetCookies(ctx, url)
	return body, err
}

// GetWebSourceWithSetCookies fetches content and also returns the Set-Cookie
// header values from the response (needed by QR login, where SESSDATA arrives
// via HttpOnly Set-Cookie rather than the callback URL query).
func (c *HTTPClient) GetWebSourceWithSetCookies(ctx context.Context, url string) (string, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}

	req.Header.Set("User-Agent", c.currentUserAgent())

	cookieVal := ""
	if c.cookieFn != nil {
		cookieVal = c.cookieFn()
	}
	if strings.Contains(url, "/ep") || strings.Contains(url, "/ss") {
		cookieVal += ";CURRENT_FNVAL=4048;"
	}
	if cookieVal != "" {
		if c.maySendCredentials(url) {
			req.Header.Set("Cookie", cookieVal)
		} else {
			LogWarn("目标主机不在可信凭据范围内，已跳过 Cookie: %s", MaskUrl(url))
		}
	}
	if strings.Contains(url, "api.bilibili.com") {
		req.Header.Set("Referer", "https://www.bilibili.com/")
	}
	if strings.Contains(url, "api.bilibili.tv") {
		req.Header.Set("sec-ch-ua", "\"Google Chrome\";v=\"131\", \"Chromium\";v=\"131\", \"Not_A Brand\";v=\"24\"")
	}
	req.Header.Set("Cache-Control", "no-cache")

	if c.debugFn != nil {
		c.debugFn("GET %s", MaskUrl(url))
	}

	var resp *http.Response
	retries := c.retries
	if retries <= 0 {
		retries = apiRetries
	}
	// Retry only transient failures — transport errors and 5xx. A 4xx is
	// deterministic, so retrying it would just burn the budget and delay the
	// error the caller needs to see (upstream retries min(MaxRetryCount,3) times
	// with exponential backoff).
	attemptReq := req
	riskAttempts := 0
	for attempt := 0; ; attempt++ {
		resp, err = c.client.Do(attemptReq)

		// 风控（412）：先换一个自动 UA，换成功才值得退避重试——显式 --user-agent 换不掉，
		// 带着同一个 UA 连打只会加重风控（任务 C）。
		if err == nil && resp.StatusCode == http.StatusPreconditionFailed {
			riskAttempts++
			if riskAttempts >= riskControlMaxAttempts || ctx.Err() != nil {
				break // 用尽风控尝试：按 4xx 原样抛出（下面统一处理）
			}
			if !c.rotateAutomaticUserAgent() {
				if c.debugFn != nil {
					c.debugFn("GET %s 被风控拦截(412)，UA 为显式指定、无法轮换，不再重试", MaskUrl(url))
				}
				break
			}
			backoff := RiskControlBackoff(riskAttempts, riskControlBackoffBase, riskControlBackoffMax)
			if c.debugFn != nil {
				c.debugFn("GET %s 被风控拦截(412)，%v 后重试（第 %d/%d 次，已轮换自动 UA）", MaskUrl(url),
					backoff, riskAttempts+1, riskControlMaxAttempts)
			}
			resp.Body.Close()
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			attemptReq = req.Clone(ctx)
			attemptReq.Header.Set("User-Agent", c.currentUserAgent())
			continue
		}

		if err == nil && resp.StatusCode < http.StatusInternalServerError {
			break
		}
		if attempt >= retries-1 || ctx.Err() != nil {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		backoff := apiRetryBackoff << attempt
		if c.debugFn != nil {
			c.debugFn("GET %s 失败（第 %d 次），%v 后重试", MaskUrl(url), attempt+1, backoff)
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		attemptReq = req.Clone(ctx)
	}
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	// Calibrate the clock from the server's own Date header: WBI signing rejects
	// a drifted clock, which would break every download on that machine.
	if c.mayCalibrateClock(url) {
		NoteServerDate(resp.Header.Get("Date"))
	}

	// Upstream accepts any 2xx (EnsureSuccessStatusCode).
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("HTTP %d: %s%s", resp.StatusCode, MaskUrl(url), statusSuffix(resp.StatusCode))
	}

	body, err := ReadAllBounded(resp.Body, maxResponseBodyBytes)
	if err != nil {
		return "", nil, err
	}

	result := string(body)
	if c.debugFn != nil {
		truncated := result
		if len(truncated) > 1024 {
			truncated = truncated[:1024] + fmt.Sprintf("…[truncated, total %d chars]", len(result))
		}
		c.debugFn("Response: %s", truncated)
	}
	return result, resp.Header.Values("Set-Cookie"), nil
}

// GetWebLocation follows redirects and returns the final URL.
func (c *HTTPClient) GetWebLocation(ctx context.Context, url string) (string, error) {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return url, err
		}
		req.Header.Set("User-Agent", c.currentUserAgent())
		req.Header.Set("Cache-Control", "no-cache")

		resp, err := c.client.Do(req)
		if err != nil {
			if method == http.MethodHead {
				if c.debugFn != nil {
					c.debugFn("HEAD request failed, trying GET")
				}
				continue
			}
			return url, err
		}
		resp.Body.Close()
		if resp.Request != nil && resp.Request.URL != nil {
			return resp.Request.URL.String(), nil
		}
		return url, nil
	}
	return url, nil
}

// PostResponse posts binary data and returns the response body.
func (c *HTTPClient) PostResponse(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Body = io.NopCloser(strings.NewReader(string(body)))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/grpc")
	}

	if headers != nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	} else {
		req.Header.Set("User-Agent", "Dalvik/2.1.0 (Linux; U; Android 6.0.1; oneplus a5010 Build/V417IR) 6.10.0 os/android model/oneplus a5010 mobi_app/android build/6100500 channel/bili innerVer/6100500 osVer/6.0.1 network/2")
		req.Header.Set("grpc-encoding", "gzip")
	}

	if c.debugFn != nil {
		c.debugFn("POST %s (%d bytes)", url, len(body))
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d from %s%s", resp.StatusCode, MaskUrl(url), statusSuffix(resp.StatusCode))
	}

	return ReadAllBounded(resp.Body, maxResponseBodyBytes)
}

// RandomUserAgent returns a freshly generated random user agent string,
// shared by all components that need one (downloads, DRM license requests, …).
func RandomUserAgent() string {
	return randomUserAgent()
}

// UserAgent returns the current user agent string.
func (c *HTTPClient) UserAgent() string {
	return c.currentUserAgent()
}

// currentUserAgent 读当前 UA（轮换可能发生在并发请求中）。
func (c *HTTPClient) currentUserAgent() string {
	c.uaMu.RLock()
	defer c.uaMu.RUnlock()
	return c.userAgent
}

// rotateAutomaticUserAgent 在「UA 不是用户显式指定」时换一个自动 UA，返回是否真的换了。
// 显式 --user-agent（例如登录态下伪装某个浏览器）必须保持稳定，否则反而更像异常客户端。
func (c *HTTPClient) rotateAutomaticUserAgent() bool {
	c.uaMu.Lock()
	defer c.uaMu.Unlock()
	if c.uaExplicit {
		return false
	}
	c.userAgent = randomUserAgent()
	return true
}

// RotateAutomaticUserAgent 轮换自动 UA（412 风控用），返回是否真的换了：显式 --user-agent
// 时返回 false，调用方据此放弃重试（下载层的 412 策略与 API 层同源）。
func (c *HTTPClient) RotateAutomaticUserAgent() bool { return c.rotateAutomaticUserAgent() }

// SetUserAgent sets a custom user agent.
func (c *HTTPClient) SetUserAgent(ua string) {
	if ua == "" {
		return
	}
	c.uaMu.Lock()
	defer c.uaMu.Unlock()
	c.userAgent = ua
	c.uaExplicit = true // 显式指定：412 时不再自动轮换（见 rotateAutomaticUserAgent）
}

// SetCookieFn overrides the cookie provider (e.g. after loading credentials
// from BBDown.data files in the workflow).
func (c *HTTPClient) SetCookieFn(fn func() string) {
	c.cookieFn = fn
}

// DownloadClient returns an http.Client sharing this client's transport (TLS
// config, proxy, connection pool) but WITHOUT the overall request timeout.
// Media URLs legitimately redirect to CDN hosts that are not Bilibili domains,
// so this client is deliberately not given the credential redirect guard; it is
// not used for credential-bearing API calls.
// media downloads can legitimately take much longer than the 2-minute API
// timeout, and cancellation is driven by context instead.
func (c *HTTPClient) DownloadClient() *http.Client {
	if tr, ok := c.client.Transport.(*http.Transport); ok {
		return &http.Client{Transport: tr.Clone()}
	}
	return &http.Client{Transport: c.client.Transport}
}

// PostForm posts form-encoded data and returns the response body.
func (c *HTTPClient) PostForm(ctx context.Context, urlStr string, form url.Values) ([]byte, error) {
	body := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.currentUserAgent())

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d from %s%s", resp.StatusCode, MaskUrl(urlStr), statusSuffix(resp.StatusCode))
	}
	return ReadAllBounded(resp.Body, maxResponseBodyBytes)
}
