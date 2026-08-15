package parser

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/QC3284/BBDown/internal/util"
)

// md5Hex computes lowercase hex md5 of s (shared WBI hashing).
func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// CheckLoginWithDetails queries the nav API to detect the login state and to
// extract the WBI mixin key. Matches upstream BBDownUtil.CheckLoginWithDetails:
// the WBI key is extracted BEFORE the login-state check, because nav returns
// wbi_img even for anonymous users (code=-101).
func CheckLoginWithDetails(ctx context.Context, client *util.HTTPClient, cookie string) (isLoggedIn, cookieExpired bool, newWbi string) {
	api := "https://api.bilibili.com/x/web-interface/nav"
	source, err := client.GetWebSource(ctx, api)
	if err != nil {
		util.LogDebug("检测登录状态失败: %v", err)
		return false, false, ""
	}

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(source), &root); err != nil {
		util.LogDebug("检测登录状态失败: %v", err)
		return false, false, ""
	}

	newWbi = ExtractWbiKey(root)
	if newWbi != "" {
		util.LogDebug("wbi: %s", newWbi)
	}

	code, _ := root["code"].(float64)
	if int(code) == -101 {
		// nav returns -101 both for "never logged in" and for "cookie expired";
		// distinguish by whether a local cookie actually exists.
		hasCookie := strings.TrimSpace(cookie) != ""
		if hasCookie {
			util.LogDebug("Cookie 已过期或无效 (code=-101)")
		} else {
			util.LogDebug("尚未登录 (code=-101，本地无 Cookie)")
		}
		return false, hasCookie, newWbi
	}

	data, _ := root["data"].(map[string]interface{})
	isLogin, _ := data["isLogin"].(bool)
	return isLogin, false, newWbi
}

// ExtractWbiKey extracts the WBI mixin key from a nav response:
// Wbi = GetMixinKey(basename(img_url) + basename(sub_url)). Returns "" when
// missing (callers keep the previous key, matching upstream).
func ExtractWbiKey(navRoot map[string]interface{}) string {
	data, ok := navRoot["data"].(map[string]interface{})
	if !ok {
		return ""
	}
	wbiImg, ok := data["wbi_img"].(map[string]interface{})
	if !ok {
		return ""
	}
	imgURL, _ := wbiImg["img_url"].(string)
	subURL, _ := wbiImg["sub_url"].(string)
	if imgURL == "" || subURL == "" {
		util.LogDebug("nav 响应中缺少 wbi_img，跳过 wbi 密钥更新")
		return ""
	}
	return util.GetMixinKey(util.RSubString(imgURL) + util.RSubString(subURL))
}
