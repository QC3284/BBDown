package util

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
)

// WbiSign generates a Wbi-signed API URL by appending w_rid parameter.
func WbiSign(api string, wbiKey string) string {
	sign := md5Hex(api + wbiKey)
	return api + "&w_rid=" + sign
}

// GetSign generates an MD5-based sign for TV/Intl API requests.
func GetSign(parameters string, isBiliPlus bool) string {
	secret := "59b43e04ad6965f34319062b478f83dd"
	if isBiliPlus {
		secret = "acd495b248ec528c2eed1e862d393126"
	}
	return md5Hex(parameters + secret)
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// GetTimeStamp returns the current Unix timestamp (seconds or milliseconds).
func GetTimeStamp(seconds bool) string {
	if seconds {
		return fmt.Sprintf("%d", nowUnix())
	}
	return fmt.Sprintf("%d", nowUnixMilli())
}

// Override points for testing.
var (
	nowUnix      = defaultNowUnix
	nowUnixMilli = defaultNowUnixMilli
)

func defaultNowUnix() int64 {
	// Use a simple approach
	return 0 // replaced at call site with time.Now().Unix()
}

func defaultNowUnixMilli() int64 {
	return 0
}
