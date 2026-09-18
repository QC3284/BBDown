package workflow

import (
	"strings"
	"testing"
)

// 本文件把上游 BBDown.Tests/PageSelectionTests.cs 的数据表整表搬过来当差分测试：
// 上游测试就是行为规格，逐条跑一遍即可撞出「本仓与上游不一致」的分P选择行为。

func TestUpstreamParsePageSelectionSucceeds(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1", "1"},
		{"1,2,10", "1,2,10"},
		{"1-3", "1,2,3"},
		{"5-5", "5"},
		{"1-3,7", "1,2,3,7"},
		{"2,4-6", "2,4,5,6"},
		{"1-2,5,8-9", "1,2,5,8,9"},
		{" 1 - 3 , 9 ", "1,2,3,9"},
	}
	for _, c := range cases {
		got, err := parsePageSelection(c.in)
		if err != nil {
			t.Errorf("parsePageSelection(%q) 报错，上游视为合法: %v", c.in, err)
			continue
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("parsePageSelection(%q) = %v, 上游期望 %s", c.in, got, c.want)
		}
	}
}

func TestUpstreamParsePageSelectionRejects(t *testing.T) {
	cases := []struct {
		in  string
		msg string
	}{
		{"10-1", "起始值大于结束值"},
		{"1-99999999", "展开后超过"},
		{"1-60000,1-60000", "总量超过"},
		{"abc-def", ""},
		{"1-", ""},
		{"1-x", ""},
		{",", ""},
		{",,", ""},
		{"  ", ""},
		{"-5", ""},
		{"0", ""},
		{"-1-5", ""},
		{"0-5", ""},
		{"3,abc", ""},
		{"abc", ""},
		{"1,2,x", ""},
	}
	for _, c := range cases {
		got, err := parsePageSelection(c.in)
		if err == nil {
			t.Errorf("parsePageSelection(%q) = %v，上游要求报错", c.in, got)
			continue
		}
		if c.msg != "" && !strings.Contains(err.Error(), c.msg) {
			t.Errorf("parsePageSelection(%q) 错误信息 %q，上游含 %q", c.in, err, c.msg)
		}
	}
}

// TestUpstreamExpandPageAliases 搬上游 ExpandPageAliases 的全部 InlineData。
func TestUpstreamExpandPageAliases(t *testing.T) {
	cases := []struct {
		in    string
		pages int
		want  string
	}{
		{"LATEST", 5, "5"},
		{"LAST", 5, "5"},
		{"NEW", 5, "5"},
		{"1,LATEST", 5, "1,5"},
		{"LATEST,3", 5, "5,3"},
		{"1-2,LATEST", 5, "1-2,5"},
		{"1-LATEST", 5, "1-5"},
		{"1-LAST", 5, "1-5"},
		{"1-NEW", 5, "1-5"},
		{"2 - LATEST", 5, "2-5"},
		{"1-2, 4-LATEST", 6, "1-2,4-6"},
		{" latest ", 5, "5"},
		{"1,LATEST,3", 2, "1,2,3"},
		// 含别名字样的普通段不能被误替换（LASTING / PLASTER 里都有 LAST）
		{"3,LASTING,2", 7, "3,LASTING,2"},
		{"8,PLASTER", 9, "8,PLASTER"},
		{"1-PLASTER", 9, "1-PLASTER"},
	}
	for _, c := range cases {
		if got := expandPageAliases(c.in, c.pages); got != c.want {
			t.Errorf("expandPageAliases(%q, %d) = %q, 上游期望 %q", c.in, c.pages, got, c.want)
		}
	}

	// 上游 ExpandPageAliases_RangeAliases_ParsesCorrectly：1-LATEST 展开后要能解析出全部分P
	pages, err := parsePageSelection(expandPageAliases("1-LATEST", 5))
	if err != nil {
		t.Fatalf("1-LATEST 展开后解析失败: %v", err)
	}
	if strings.Join(pages, ",") != "1,2,3,4,5" {
		t.Errorf("1-LATEST → %v，上游期望 1..5", pages)
	}
}
