package login

import (
	"strings"
)

// cookieAttributeNames are Set-Cookie attribute keys (not cookie fields) that
// must be dropped when merging (upstream MergeLoginCookies).
var cookieAttributeNames = map[string]bool{
	"path":        true,
	"domain":      true,
	"expires":     true,
	"max-age":     true,
	"secure":      true,
	"httponly":    true,
	"samesite":    true,
	"priority":    true,
	"partitioned": true,
	"size":        true,
}

// canonicalCookieNames maps lowercase cookie names to bilibili's canonical
// casing. The passport callback URL / Set-Cookie headers can deliver lowercase
// names (e.g. "sessdata"), which bilibili's nav API rejects case-sensitively:
// the cookie must be stored in canonical case or the login check reports the
// cookie as expired.
var canonicalCookieNames = map[string]string{
	"sessdata":          "SESSDATA",
	"dedeuserid":        "DedeUserID",
	"dedeuserid__ckmd5": "DedeUserID__ckMd5",
}

func canonicalName(name string) string {
	if n, ok := canonicalCookieNames[strings.ToLower(name)]; ok {
		return n
	}
	return name
}

// NormalizeCookieNames rewrites cookie field names to bilibili's canonical
// casing (case-insensitive). Used when loading BBDown.data so cookies saved
// with non-canonical case (older builds, hand-edited files) still work.
func NormalizeCookieNames(cookieStr string) string {
	parts := strings.Split(cookieStr, ";")
	for i, p := range parts {
		n, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || n == "" {
			continue
		}
		if cn, found := canonicalCookieNames[strings.ToLower(n)]; found {
			parts[i] = cn + "=" + v
		}
	}
	return strings.Join(parts, ";")
}

// MergeLoginCookies merges cookies from a callback URL query string with
// Set-Cookie header values (upstream MergeLoginCookies): query k=v pairs and
// Set-Cookie fields are combined case-insensitively (later wins), cookie
// attributes are dropped, and every value has "," replaced with "%2C".
func MergeLoginCookies(cookieQuery string, setCookies []string) string {
	fields := make(map[string]string)

	addQuery := func(q string) {
		for _, kv := range strings.Split(q, "&") {
			if kv == "" {
				continue
			}
			name, value, found := strings.Cut(kv, "=")
			if !found {
				continue
			}
			value = strings.ReplaceAll(value, ",", "%2C")
			fields[strings.ToLower(name)] = value
		}
	}
	addQuery(cookieQuery)

	for _, sc := range setCookies {
		// First segment before ';' is the name=value field.
		first := sc
		if idx := strings.Index(sc, ";"); idx >= 0 {
			first = sc[:idx]
		}
		name, value, found := strings.Cut(strings.TrimSpace(first), "=")
		if !found {
			continue
		}
		lname := strings.ToLower(strings.TrimSpace(name))
		if cookieAttributeNames[lname] {
			continue
		}
		value = strings.ReplaceAll(value, ",", "%2C")
		fields[lname] = value
	}

	// Preserve the query's original field order where possible (map iteration
	// would randomize); rebuild from the query first, then append the rest.
	var ordered []string
	seen := make(map[string]bool)
	for _, kv := range strings.Split(cookieQuery, "&") {
		if kv == "" {
			continue
		}
		name, _, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		lname := strings.ToLower(name)
		if seen[lname] {
			continue
		}
		seen[lname] = true
		ordered = append(ordered, canonicalName(name)+"="+fields[lname])
	}
	for name, value := range fields {
		if !seen[name] {
			ordered = append(ordered, canonicalName(name)+"="+value)
		}
	}
	return strings.Join(ordered, ";")
}

// GetCookieValue extracts a cookie value by name (case-insensitive).
func GetCookieValue(cookieStr, name string) string {
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, v, found := strings.Cut(part, "=")
		if found && strings.EqualFold(strings.TrimSpace(n), name) {
			return v
		}
	}
	return ""
}
