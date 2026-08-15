package util

import (
	"context"
	"strings"
	"time"
)

// CheckUpdateAsync probes the GitHub latest-release redirect and logs when a
// newer version exists (upstream CheckUpdateAsync, fire-and-forget).
func CheckUpdateAsync(ctx context.Context, client *HTTPClient, currentVersion string) {
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		redirect, err := client.GetWebLocation(ctx, "https://github.com/QC3284/BBDown/releases/latest")
		if err != nil {
			LogDebug("检查更新失败: %v", err)
			return
		}
		latest := strings.TrimPrefix(redirect, "https://github.com/QC3284/BBDown/releases/tag/")
		if latest != "" && !strings.HasPrefix(latest, "https") && !strings.EqualFold(latest, currentVersion) {
			LogColor("发现新版本：%s", latest)
		}
	}()
}
