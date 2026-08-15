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
	"time"
)

// HTTPClient wraps the standard http.Client with BBDown-specific behavior.
type HTTPClient struct {
	client    *http.Client
	userAgent string
	debugFn   func(string, ...interface{})
	skipSSL   func() bool
	cookieFn  func() string
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
			Transport: transport,
			Timeout:   2 * time.Minute,
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
		fmt.Sprintf("AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", randomVersion(80, 110)),
		fmt.Sprintf("Gecko/20100101 Firefox/%s", randomVersion(80, 110)),
	}
	platform := platforms[rand.Intn(len(platforms))]
	return fmt.Sprintf("Mozilla/5.0 (%s) %s", platform, browsers[rand.Intn(len(browsers))])
}

// GetWebSource fetches the content from a URL as a string.
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

	req.Header.Set("User-Agent", c.userAgent)

	cookieVal := ""
	if c.cookieFn != nil {
		cookieVal = c.cookieFn()
	}
	if strings.Contains(url, "/ep") || strings.Contains(url, "/ss") {
		cookieVal += ";CURRENT_FNVAL=4048;"
	}
	if cookieVal != "" {
		req.Header.Set("Cookie", cookieVal)
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

	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	// Upstream accepts any 2xx (EnsureSuccessStatusCode).
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, MaskUrl(url))
	}

	body, err := io.ReadAll(resp.Body)
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
		req.Header.Set("User-Agent", c.userAgent)
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
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, MaskUrl(url))
	}

	return io.ReadAll(resp.Body)
}

// RandomUserAgent returns a freshly generated random user agent string,
// shared by all components that need one (downloads, DRM license requests, …).
func RandomUserAgent() string {
	return randomUserAgent()
}

// UserAgent returns the current user agent string.
func (c *HTTPClient) UserAgent() string {
	return c.userAgent
}

// SetUserAgent sets a custom user agent.
func (c *HTTPClient) SetUserAgent(ua string) {
	if ua != "" {
		c.userAgent = ua
	}
}

// SetCookieFn overrides the cookie provider (e.g. after loading credentials
// from BBDown.data files in the workflow).
func (c *HTTPClient) SetCookieFn(fn func() string) {
	c.cookieFn = fn
}

// DownloadClient returns an http.Client sharing this client's transport (TLS
// config, proxy, connection pool) but WITHOUT the overall request timeout:
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
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, MaskUrl(urlStr))
	}
	return io.ReadAll(resp.Body)
}
