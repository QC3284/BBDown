package util

import "testing"

// 本文件搬上游 ServeCommandTests 的官方域名表（HTTPUtil.OfficialHostSuffixes 是
// 全项目凭据外发校验、重定向守卫、serve host 净化的唯一定义源）。

func TestUpstreamOfficialHostSuffixes(t *testing.T) {
	allowed := []string{
		"bilibili.com", "api.bilibili.com", "data.api.bilibili.com",
		"b23.tv",
		"bilivideo.com", "upos-sz-mirrorcoso1.bilivideo.com",
		"hdslb.com", "i0.hdslb.com",
		"biliapi.net", "grpc.biliapi.net",
		"biliapi.com",
		"bilibili.tv",
		"biliintl.com",
		"aisee.tv", "snm0516.aisee.tv",
	}
	for _, host := range allowed {
		if !IsTrustedCookieHost(host) {
			t.Errorf("%q 是官方域名，应可信", host)
		}
	}

	for _, host := range []string{"evil.com", "notbilibili.com", "bilibili.com.evil.com", ""} {
		if IsTrustedCookieHost(host) {
			t.Errorf("%q 不是官方域名，不应可信", host)
		}
	}
}
