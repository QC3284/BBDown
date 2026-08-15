package login

import "testing"

func TestNormalizeCookieNames(t *testing.T) {
	in := "sessdata=abc%2Cdef;DedeUserID=42;dedeuserid__ckmd5=xx;bili_jct=yz;sid=s1"
	out := NormalizeCookieNames(in)
	want := "SESSDATA=abc%2Cdef;DedeUserID=42;DedeUserID__ckMd5=xx;bili_jct=yz;sid=s1"
	if out != want {
		t.Fatalf("NormalizeCookieNames = %q, want %q", out, want)
	}
}

func TestMergeLoginCookiesCanonicalCase(t *testing.T) {
	// 小写字段名（护照回调真实情况）必须输出规范大小写。
	cookie := MergeLoginCookies("sessdata=abc%2Cdef&bili_jct=xyz&DedeUserID=42", nil)
	for _, want := range []string{"SESSDATA=abc%2Cdef", "bili_jct=xyz", "DedeUserID=42"} {
		if !containsField(cookie, want) {
			t.Fatalf("merged cookie %q missing field %q", cookie, want)
		}
	}
	if containsField(cookie, "sessdata=") {
		t.Fatalf("merged cookie %q still has lowercase sessdata", cookie)
	}
}

func TestMergeLoginCookiesKeepsValueEquals(t *testing.T) {
	// 值中可能包含 '='，切割只应发生在第一个 '=' 处。
	cookie := MergeLoginCookies("sessdata=a=b=c", nil)
	if !containsField(cookie, "SESSDATA=a=b=c") {
		t.Fatalf("merged cookie %q should keep value 'a=b=c'", cookie)
	}
}

func containsField(cookie, field string) bool {
	for _, p := range splitFields(cookie) {
		if p == field {
			return true
		}
	}
	return false
}

func splitFields(cookie string) []string {
	var out []string
	start := 0
	for i := 0; i < len(cookie); i++ {
		if cookie[i] == ';' {
			out = append(out, cookie[start:i])
			start = i + 1
		}
	}
	if start < len(cookie) {
		out = append(out, cookie[start:])
	}
	return out
}
