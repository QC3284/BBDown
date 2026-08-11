package login

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

const (
	webGenerateURL  = "https://passport.bilibili.com/x/passport-login/web/qrcode/generate?source=main-fe-header"
	webPollURL      = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=%s&source=main-fe-header"
	tvAuthURL       = "https://passport.snm0516.aisee.tv/x/passport-tv-login/qrcode/auth_code"
	tvPollURL       = "https://passport.bilibili.com/x/passport-tv-login/qrcode/poll"
	tvAppKey        = "4409e2ce8ffd12b8"
	tvAppSecret     = "59b43e04ad6965f34319062b478f83dd"
)

// LoginWeb performs WEB account login via QR code scanning.
func LoginWeb(client *util.HTTPClient) error {
	util.Log("获取登录地址...")

	// Step 1: Generate QR code
	resp, err := client.GetWebSource(nil, webGenerateURL)
	if err != nil {
		return fmt.Errorf("获取二维码失败: %w", err)
	}

	var genResult struct {
		Code int `json:"code"`
		Data struct {
			URL       string `json:"url"`
			QrcodeKey string `json:"qrcode_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &genResult); err != nil {
		return fmt.Errorf("解析二维码响应: %w", err)
	}
	if genResult.Code != 0 {
		return fmt.Errorf("生成二维码失败: code=%d", genResult.Code)
	}

	qrURL := genResult.Data.URL
	qrcodeKey := genResult.Data.QrcodeKey

	// Step 2: Print QR code to terminal and save to file
	util.Log("生成二维码...")
	if err := util.PrintQRCode(qrURL); err != nil {
		util.LogWarn("终端二维码打印失败: %v", err)
	}
	util.Log("请使用Bilibili APP扫描上方二维码登录")

	// Step 3: Poll for scan status
	scanned := false
	for {
		time.Sleep(1 * time.Second)

		pollResp, err := client.GetWebSource(nil, fmt.Sprintf(webPollURL, qrcodeKey))
		if err != nil {
			util.LogWarn("轮询失败: %v", err)
			continue
		}

		var pollResult struct {
			Code int `json:"code"`
			Data struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				URL     string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(pollResp), &pollResult); err != nil {
			continue
		}

		switch pollResult.Data.Code {
		case 86038:
			util.LogColor("%s", "二维码已过期, 请重新执行登录指令.")
			_ = os.Remove("qrcode.png")
			return fmt.Errorf("二维码已过期")
		case 86101:
			// Waiting for scan
			continue
		case 86090:
			// Scanned, waiting for confirm
			if !scanned {
				util.Log("扫码成功, 请确认...")
				scanned = true
			}
		default:
			// Login success
			callbackURL := pollResult.Data.URL
			if callbackURL == "" {
				util.LogError("登录成功但回调 URL 为空")
				return fmt.Errorf("回调URL为空")
			}

			// Extract SESSDATA from URL query
			parsed, err := url.Parse(callbackURL)
			if err != nil {
				return fmt.Errorf("解析回调URL失败: %w", err)
			}
			queryStr := parsed.RawQuery
			if queryStr == "" {
				util.LogError("登录成功但回调 URL 未包含 cookie 参数")
				return fmt.Errorf("未获取到cookie")
			}

			sessdata := parsed.Query().Get("SESSDATA")
			util.Log("登录成功: SESSDATA=%s", maskValue(sessdata))

			// Save cookie to BBDown.data
			cookieStr := strings.ReplaceAll(queryStr, "&", ";")
			cookieStr = strings.ReplaceAll(cookieStr, ",", "%2C")

			cookiePath := filepath.Join(appDir(), "BBDown.data")
			if err := os.WriteFile(cookiePath, []byte(cookieStr), 0o600); err != nil {
				return fmt.Errorf("保存cookie失败: %w", err)
			}
			util.Log("Cookie 已保存到 %s", cookiePath)
			_ = os.Remove("qrcode.png")
			return nil
		}
	}
}

// LoginTV performs TV account login via QR code scanning.
func LoginTV(client *util.HTTPClient) error {
	util.Log("获取TV登录地址...")

	// Step 1: Build TV login parameters
	params := getTVLoginParams()
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	// Send auth_code request
	respBytes, err := client.PostForm(nil, tvAuthURL, form)
	if err != nil {
		return fmt.Errorf("获取TV认证码失败: %w", err)
	}

	var authResult struct {
		Code int `json:"code"`
		Data struct {
			URL      string `json:"url"`
			AuthCode string `json:"auth_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &authResult); err != nil {
		return fmt.Errorf("解析TV认证响应: %w", err)
	}

	qrURL := authResult.Data.URL
	authCode := authResult.Data.AuthCode

	// Step 2: Print QR code
	util.Log("生成二维码...")
	if err := util.PrintQRCode(qrURL); err != nil {
		util.LogWarn("终端二维码打印失败: %v", err)
	}
	util.Log("请使用Bilibili APP扫描上方二维码登录TV账号")

	// Update params for polling
	params["auth_code"] = authCode
	params["ts"] = fmt.Sprintf("%d", time.Now().Unix())
	delete(params, "sign")
	params["sign"] = getTVSign(params)

	// Step 3: Poll
	for {
		time.Sleep(1 * time.Second)

		form := url.Values{}
		for k, v := range params {
			form.Set(k, v)
		}

		respBytes, err := client.PostForm(nil, tvPollURL, form)
		if err != nil {
			util.LogWarn("轮询失败: %v", err)
			continue
		}

		var pollResult struct {
			Code json.Number `json:"code"`
			Data struct {
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(respBytes, &pollResult); err != nil {
			continue
		}

		codeStr := string(pollResult.Code)

		switch codeStr {
		case "86038":
			util.LogColor("%s", "二维码已过期, 请重新执行登录指令.")
			_ = os.Remove("qrcode.png")
			return fmt.Errorf("TV二维码已过期")
		case "86039":
			// Waiting for scan
			continue
		default:
			accessToken := pollResult.Data.AccessToken
			util.Log("登录成功: AccessToken=%s", maskValue(accessToken))

			tvTokenPath := filepath.Join(appDir(), "BBDownTV.data")
			if err := os.WriteFile(tvTokenPath, []byte("access_token="+accessToken), 0o600); err != nil {
				return fmt.Errorf("保存TV token失败: %w", err)
			}
			util.Log("TV Token 已保存到 %s", tvTokenPath)
			_ = os.Remove("qrcode.png")
			return nil
		}
	}
}

// getTVLoginParams builds the parameter collection for TV login.
func getTVLoginParams() map[string]string {
	now := time.Now()
	deviceID := randomString(20)
	buvid := randomString(37)
	fingerprint := now.Format("20060102150405.000") + randomString(45)

	params := map[string]string{
		"appkey":          tvAppKey,
		"auth_code":       "",
		"bili_local_id":   deviceID,
		"build":           "102801",
		"buvid":           buvid,
		"channel":         "master",
		"device":          "OnePlus",
		"device_id":       deviceID,
		"device_name":     "OnePlus7TPro",
		"device_platform": "Android10OnePlusHD1910",
		"fingerprint":     fingerprint,
		"guid":            buvid,
		"local_fingerprint": fingerprint,
		"local_id":        buvid,
		"mobi_app":        "android_tv_yst",
		"networkstate":    "wifi",
		"platform":        "android",
		"sys_ver":         "29",
		"ts":              fmt.Sprintf("%d", time.Now().Unix()),
	}
	params["sign"] = getTVSign(params)

	return params
}

func getTVSign(params map[string]string) string {
	// Build query string (sorted by key for consistency)
	values := url.Values{}
	for k, v := range params {
		if k == "sign" {
			continue
		}
		values.Set(k, v)
	}
	queryStr := values.Encode()
	toEncode := queryStr + tvAppSecret
	h := md5.Sum([]byte(toEncode))
	return hex.EncodeToString(h[:])
}

func maskValue(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-4:]
}

func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

var randomChars = []rune("ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_0123456789")

func randomString(length int) string {
	b := make([]rune, length)
	for i := range b {
		b[i] = randomChars[rand.Intn(len(randomChars))]
	}
	return string(b)
}
