package util

import (
	"net/url"
	"strings"
)

// SensitiveDataMasker equivalent: masks credentials in log output.

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

// sensitiveQueryKeys are query parameter names that carry credentials.
var sensitiveQueryKeys = map[string]bool{
	"access_key":        true,
	"access_token":      true,
	"refresh_token":     true,
	"token":             true,
	"sessdata":          true,
	"bili_jct":          true,
	"dedeuserid__ckmd5": true,
	"dedeuserid":        true,
	"sid":               true,
}

// MaskUrl masks sensitive query parameter values in a URL string.
func MaskUrl(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	changed := false
	for k := range q {
		lk := strings.ToLower(k)
		if sensitiveQueryKeys[lk] {
			vals := q[k]
			for i, v := range vals {
				if v != "" {
					vals[i] = MaskValue(v)
					changed = true
				}
			}
			q[k] = vals
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// MaskCookie masks credential values in a Cookie header string.
func MaskCookie(cookie string) string {
	if cookie == "" {
		return cookie
	}
	parts := strings.Split(cookie, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, value, found := strings.Cut(p, "=")
		if !found {
			out = append(out, p)
			continue
		}
		lname := strings.ToLower(strings.TrimSpace(name))
		switch lname {
		case "sessdata", "bili_jct", "dedeuserid", "dedeuserid__ckmd5", "access_token", "refresh_token", "sid", "buvid3":
			out = append(out, name+"="+MaskValue(value))
		default:
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}
