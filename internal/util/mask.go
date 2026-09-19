package util

import "strings"

// SensitiveDataMasker 等价物：对日志里的凭据做脱敏（上游 BBDown.Core/Util/SensitiveDataMasker）。
//
// MaskValue masks a secret value: empty stays empty, <=8 chars becomes "***",
// otherwise first 4 + "***" + last 4 (upstream SensitiveDataMasker.MaskValue).
func MaskValue(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-4:]
}

// sensitiveKeys 是需要脱敏的键名（URL query 与 Cookie 共用同一张表，上游 SensitiveKeys，
// 比较时不区分大小写）。
//
// 除账号凭据外还包含带签名媒体 URL（playurl/CDN）的临时授权参数：sign/x_sign/w_rid 是 CDN
// 下载票据、deadline 是时效戳、marlin_token 是 DRM 许可令牌——这些 URL 经解析器流转到下载器，
// 日志若明文落盘就等于把「用户可用的临时下载权（含付费内容）」写进了日志文件（上游 B3-F1）。
var sensitiveKeys = map[string]bool{
	"access_key":        true,
	"access_token":      true,
	"refresh_token":     true,
	"token":             true,
	"sessdata":          true,
	"bili_jct":          true,
	"dedeuserid":        true,
	"dedeuserid__ckmd5": true,
	"sid":               true,
	"sign":              true,
	"x_sign":            true,
	"w_rid":             true,
	"deadline":          true,
	"marlin_token":      true,
	"marlintoken":       true,
}

func isSensitiveKey(key string) bool {
	return sensitiveKeys[strings.ToLower(strings.TrimSpace(key))]
}

// MaskUrl masks sensitive query parameter values in a URL string.
//
// 逐段字符串手术，不解析也不重编码 URL：上游同样如此——用 net/url 往返一趟会把掩码里的
// "***" 编码成 %2A%2A%2A，还会改动其它参数（空格/加号/百分号编码），日志里就不再是原样 URL。
// fragment（#之后）不属于 query，原样保留。
func MaskUrl(raw string) string {
	queryStart := strings.IndexByte(raw, '?')
	if queryStart < 0 {
		return raw
	}
	prefix := raw[:queryStart+1]
	query := raw[queryStart+1:]

	fragment := ""
	if fs := strings.IndexByte(query, '#'); fs >= 0 {
		fragment = query[fs:]
		query = query[:fs]
	}

	parts := strings.Split(query, "&")
	for i, part := range parts {
		sep := strings.IndexByte(part, '=')
		if sep <= 0 {
			continue
		}
		key := part[:sep]
		if !isSensitiveKey(key) {
			continue
		}
		parts[i] = key + "=" + MaskValue(part[sep+1:])
	}
	return prefix + strings.Join(parts, "&") + fragment
}

// MaskCookie masks credential values in a Cookie header string.
//
// 与上游一致：只掩 sensitiveKeys 里的项（buvid3 这类设备标识不是凭据，保留原文便于排查），
// 分隔符与各项原有空白原样保留，值里含 '=' 时按第一个 '=' 切分、整体掩掉。
func MaskCookie(cookie string) string {
	if cookie == "" {
		return cookie
	}
	items := strings.Split(cookie, ";")
	for i, item := range items {
		sep := strings.IndexByte(item, '=')
		if sep <= 0 {
			continue
		}
		key := strings.TrimSpace(item[:sep])
		if !isSensitiveKey(key) {
			continue
		}
		leading := item[:len(item)-len(strings.TrimLeft(item, " \t"))]
		items[i] = leading + key + "=" + MaskValue(item[sep+1:])
	}
	return strings.Join(items, ";")
}
