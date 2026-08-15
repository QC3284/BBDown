package util

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
)

// WbiSign generates a Wbi-signed API URL by appending w_rid parameter.
// NOTE: kept for API compatibility; the parser signs with md5(params + Wbi) directly.
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

// mixinKeyEncTab is Bilibili's WBI mixin key index table (upstream BBDownUtil.GetMixinKey).
var mixinKeyEncTab = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
}

// GetMixinKey derives the 32-char WBI salt from the 64-char img_key+sub_key.
func GetMixinKey(orig string) string {
	if len(orig) < 64 {
		return ""
	}
	b := make([]byte, 0, 32)
	for _, idx := range mixinKeyEncTab {
		b = append(b, orig[idx])
	}
	return string(b)
}

// RSubString extracts the basename without extension: "/a/b/cd.png" -> "cd".
func RSubString(sub string) string {
	if idx := strings.LastIndex(sub, "/"); idx >= 0 {
		sub = sub[idx+1:]
	}
	if idx := strings.LastIndex(sub, "."); idx >= 0 {
		sub = sub[:idx]
	}
	return sub
}
