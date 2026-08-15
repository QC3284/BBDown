package util

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// BuvidProvider equivalent: injects buvid3 into the cookie when missing
// (upstream fetches it from /x/frontend/finger/spi).
var (
	buvidMu     sync.Mutex
	buvid3Value string
)

// HasBuvid3 reports whether the cookie already contains a buvid3 field.
func HasBuvid3(cookie string) bool {
	return cookie != "" && strings.Contains(strings.ToLower(cookie), "buvid3=")
}

// EnsureBuvid3 returns the cookie with buvid3 injected (or the original cookie
// when it already has one / the spi API fails). The fetched value is cached for
// the process lifetime (upstream uses a semaphore-guarded singleton fetch).
func EnsureBuvid3(ctx context.Context, client *HTTPClient, cookie string) string {
	if HasBuvid3(cookie) {
		return cookie
	}
	buvidMu.Lock()
	defer buvidMu.Unlock()
	if HasBuvid3(cookie) {
		return cookie
	}
	if buvid3Value != "" {
		return appendBuvid3(cookie, buvid3Value)
	}

	source, err := client.GetWebSource(ctx, "https://api.bilibili.com/x/frontend/finger/spi")
	if err == nil {
		var root struct {
			Data struct {
				B3 string `json:"b_3"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(source), &root) == nil && root.Data.B3 != "" {
			buvid3Value = root.Data.B3
			LogDebug("已获取 buvid3: %s", buvid3Value)
		}
	}
	if buvid3Value == "" {
		LogDebug("spi 接口未返回 b_3，跳过 buvid3 注入")
		return cookie
	}
	return appendBuvid3(cookie, buvid3Value)
}

func appendBuvid3(cookie, buvid3 string) string {
	if cookie == "" {
		return "buvid3=" + buvid3
	}
	return strings.TrimRight(cookie, ";") + ";buvid3=" + buvid3
}
