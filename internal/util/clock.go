package util

import (
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// WBI signatures carry a "wts" timestamp and the API rejects a request whose
// clock is more than a minute away from its own, so a machine with a drifted
// clock cannot download anything at all. The offset is derived from the Date
// response header of official hosts.

// maxClockSkew is how far the Date header may move the global offset. A wildly
// wrong value (a proxy mislabelling the date, a captive portal) must not become
// a new source of failure.
const maxClockSkew = time.Hour

var serverClockOffset atomic.Int64 // seconds

// ServerClockOffset returns the current offset in seconds.
func ServerClockOffset() int64 { return serverClockOffset.Load() }

// NoteServerDate updates the clock offset from a response Date header. It is a
// no-op for an empty/unparseable header, and any offset beyond ±1h is ignored.
func NoteServerDate(dateHeader string) {
	if dateHeader == "" {
		return
	}
	t, err := http.ParseTime(dateHeader)
	if err != nil {
		return
	}
	skew := time.Until(t).Round(time.Second)
	if skew > maxClockSkew || skew < -maxClockSkew {
		return
	}
	serverClockOffset.Store(int64(skew.Seconds()))
}

// Now returns the current time corrected by the observed server offset. All
// request timestamps must go through it.
func Now() time.Time { return time.Now().Add(time.Duration(serverClockOffset.Load()) * time.Second) }

// UnixNow returns the corrected Unix timestamp used for signing.
func UnixNow() int64 { return Now().Unix() }

// mayCalibrateClock reports whether a response may move the global clock offset:
// only official Bilibili hosts, and never over an --insecure connection (whose
// Date header comes from whoever is in the middle).
func (c *HTTPClient) mayCalibrateClock(rawURL string) bool {
	if c.skipSSL != nil && c.skipSSL() {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "bilibili.com" || strings.HasSuffix(host, ".bilibili.com")
}
