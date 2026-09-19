package login

import (
	"strings"
	"testing"
)

// 本文件搬上游 BBDownLoginUtilMergeTests：B 站新版登录（2026）把 SESSDATA 等凭证经
// poll 响应头 Set-Cookie（HttpOnly）下发，data.url 只剩 crossDomain 跳转参数——
// 合并必须同时覆盖 url query（旧协议）与 Set-Cookie（新协议），且丢掉 cookie 属性。

func TestUpstreamMergeLoginCookies(t *testing.T) {
	urlQuery := "ticket=t1&gourl=https%3A%2F%2Fwww.bilibili.com&first_domain=.bilibili.com"
	setCookies := []string{
		"SESSDATA=sess%2Cvalue; Path=/; Domain=bilibili.com; Expires=Wed, 09 Feb 2027 00:00:00 GMT; HttpOnly; Secure; SameSite=None",
		"bili_jct=bj123; Path=/; Domain=.bilibili.com; HttpOnly",
		"DedeUserID=345960772; Path=/; Domain=.bilibili.com",
	}
	merged := MergeLoginCookies(urlQuery, setCookies)

	for _, want := range []string{
		"SESSDATA=sess%2Cvalue",
		"bili_jct=bj123",
		"DedeUserID=345960772",
		"ticket=t1",
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("合并结果缺 %q: %s", want, merged)
		}
	}

	// cookie 属性不是凭证，混入会污染凭据文件（B 站在 nav 校验时会拒绝）
	for _, bad := range []string{"Path=", "Domain=", "Expires=", "HttpOnly", "SameSite"} {
		if strings.Contains(merged, bad) {
			t.Errorf("合并结果混入了 cookie 属性 %q: %s", bad, merged)
		}
	}

	// query 的 3 个字段 + Set-Cookie 的 3 个凭证字段，以 ; 连接
	if n := len(strings.Split(merged, ";")); n != 6 {
		t.Errorf("字段数 = %d，上游期望 6: %s", n, merged)
	}

	// 名称大小写不敏感：同名以 Set-Cookie（后到）为准
	merged = MergeLoginCookies("sessdata=old", []string{"SESSDATA=new"})
	if strings.Contains(merged, "old") || !strings.Contains(merged, "new") {
		t.Errorf("后到的 Set-Cookie 应覆盖 query 里的同名值: %s", merged)
	}
	if !strings.Contains(merged, "SESSDATA=") {
		t.Errorf("名称应归一为 B 站的规范大小写: %s", merged)
	}
}
