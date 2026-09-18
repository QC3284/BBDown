package util

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// EstimateSessdataExpiryDays estimates how many days the SESSDATA in a cookie
// string has left, or nil when it cannot be determined.
//
// SESSDATA carries a URL-encoded, comma-separated payload whose first field is
// base64 JSON with an "expires" timestamp. A long-running serve process would
// otherwise lose its login silently after weeks. Parsing is fail-open: an
// unknown or malformed value returns nil rather than raising a bogus warning
// (upstream EstimateSessdataExpiryDays).
func EstimateSessdataExpiryDays(cookie string) *int {
	value := sessdataValue(cookie)
	if value == "" {
		return nil
	}
	// PathUnescape, not QueryUnescape: the latter turns a literal "+" (valid in
	// base64) into a space and would corrupt the payload.
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return nil
	}
	first, _, _ := strings.Cut(decoded, ",")
	first = strings.TrimSpace(first)
	if first == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(first)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(first, "="))
		if err != nil {
			return nil
		}
	}
	var payload struct {
		Expires int64 `json:"expires"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Expires == 0 {
		return nil
	}
	days := int(time.Until(time.Unix(payload.Expires, 0)).Hours() / 24)
	return &days
}

// sessdataValue extracts the SESSDATA field from a cookie string.
func sessdataValue(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "SESSDATA") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
