package fetcher

import (
	"encoding/json"
	"testing"
)

// 本文件搬上游 JsonElementExtensionsTests：JSON 取值一律 fail-open，
// 但「整串是数字」与「数字前缀」必须区分——后者是脏数据，不能当合法编号用下去。

func parseObj(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal %s: %v", s, err)
	}
	return m
}

func TestUpstreamGetValueAsStringSafe(t *testing.T) {
	// 数字字段被字符串化（上游 GetValueAsStringSafe 的 prop.ToString()）
	if got := gs(parseObj(t, `{"code":86038}`), "code"); got != "86038" {
		t.Errorf("数字字段 → %q，上游期望 86038", got)
	}
	// 字符串字段原样读取
	if got := gs(parseObj(t, `{"code":"86039"}`), "code"); got != "86039" {
		t.Errorf("字符串字段 → %q", got)
	}
	// 字段缺失 / 值为 null → 默认空串
	for _, js := range []string{`{"other":1}`, `{"code":null}`} {
		if got := gs(parseObj(t, js), "code"); got != "" {
			t.Errorf("%s → %q，上游期望空串", js, got)
		}
	}
}

func TestUpstreamGetIntSafe(t *testing.T) {
	cases := []struct {
		json string
		want int64
	}{
		{`{"code":12345}`, 12345},
		{`{"code":"12345"}`, 12345},
		{`{"code":-101}`, -101},
		{`{"code":"-101"}`, -101},
		{`{"code":"invalid"}`, 0},
		{`{"code":null}`, 0},
		{`{"other":1}`, 0},
	}
	for _, c := range cases {
		m := parseObj(t, c.json)
		if got := int64(gi(m, "code")); got != c.want {
			t.Errorf("gi(%s) = %d，上游期望 %d", c.json, got, c.want)
		}
		if got := gi64(m, "code"); got != c.want {
			t.Errorf("gi64(%s) = %d，上游期望 %d", c.json, got, c.want)
		}
	}

	// 大整数：int64 档看得住，int32 档按上游 TryGetInt32 的语义落到默认值 0
	big := parseObj(t, `{"code":9876543210}`)
	if got := gi64(big, "code"); got != 9876543210 {
		t.Errorf("gi64 大整数 = %d，上游期望 9876543210", got)
	}
	if got := gi(parseObj(t, `{"code":9876543210}`), "code"); got != 0 {
		t.Errorf("gi 超出 int32 应取默认值 0，实际 %d", got)
	}
	if got := gi64(parseObj(t, `{"code":"9876543210"}`), "code"); got != 9876543210 {
		t.Errorf("gi64 字符串大整数 = %d", got)
	}

	// 数字前缀不是合法整数：上游 int.TryParse 要求整串，Sscanf("%d") 会把 "12abc" 当 12
	for _, bad := range []string{`{"code":"12abc"}`, `{"code":"abc12"}`, `{"code":"1.5"}`} {
		if got := gi(parseObj(t, bad), "code"); got != 0 {
			t.Errorf("脏数字串 %s 应取默认值 0，实际 %d", bad, got)
		}
		if got := gi64(parseObj(t, bad), "code"); got != 0 {
			t.Errorf("gi64 脏数字串 %s 应取默认值 0，实际 %d", bad, got)
		}
	}

	// 两侧空白与正负号照上游 NumberStyles.Integer 容忍
	if got := gi(parseObj(t, `{"code":" 42 "}`), "code"); got != 42 {
		t.Errorf("带空白数字串 = %d，上游期望 42", got)
	}
}
