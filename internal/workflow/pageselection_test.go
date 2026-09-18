package workflow

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
)

// TestParsePageSelectionCumulativeCap: the cap used to apply per range, so
// "1-60000,1-60000" passed it twice and expanded to 120000 entries — a
// serve-side amplification anyone with /add-task access could trigger.
func TestParsePageSelectionCumulativeCap(t *testing.T) {
	if _, err := parsePageSelection("1-60000,1-60000"); err == nil {
		t.Error("cumulative expansion beyond the cap must be rejected")
	}
	if _, err := parsePageSelection("1-100001"); err == nil {
		t.Error("a single oversized range must be rejected")
	}

	got, err := parsePageSelection("1-3,5,7")
	if err != nil {
		t.Fatalf("a normal selection must still parse: %v", err)
	}
	if strings.Join(got, ",") != "1,2,3,5,7" {
		t.Errorf("selection = %v", got)
	}
}

// TestExpandPageAliases: 别名必须按逗号分段、整段全词匹配。子串替换会让 -p LATEST 变成
// "5EST"（LAST 是 LATEST 的前缀），上游为此专门记录过该坑。
func TestExpandPageAliases(t *testing.T) {
	cases := []struct {
		in    string
		pages int
		want  string
	}{
		{"LAST", 5, "5"},
		{"NEW", 5, "5"},
		{"LATEST", 5, "5"},
		{"latest", 5, "5"},
		{"3,LATEST", 12, "3,12"},
		{"1-LAST", 7, "1-7"},
		{"LAST-2", 7, "7-2"},
		{"1-3", 7, "1-3"},
		{"-5", 7, "-5"},
		{" 2 , latest ", 9, "2,9"},
		// 别名必须整段匹配：嵌在别的 token 里时原样保留，让 parsePageSelection 报错；
		// 子串替换会把它悄悄变成别的页码（"1LATEST" → "112"）。
		{"1LATEST", 12, "1LATEST"},
		{"LATESTX", 12, "LATESTX"},
	}
	for _, c := range cases {
		if got := expandPageAliases(c.in, c.pages); got != c.want {
			t.Errorf("expandPageAliases(%q, %d) = %q, 期望 %q", c.in, c.pages, got, c.want)
		}
	}

	// 端到端：-p LATEST 必须真的展开成最后一页，而不是报「所选分P不存在」。
	cfg := config.DefaultMyOption()
	cfg.SelectPage = "LATEST"
	vInfo := &entity.VInfo{PagesInfo: make([]entity.Page, 12)}
	got, err := getSelectedPages(&cfg, vInfo, "")
	if err != nil {
		t.Fatalf("-p LATEST 解析失败: %v", err)
	}
	if strings.Join(got, ",") != "12" {
		t.Errorf("-p LATEST = %v, 期望 [12]", got)
	}

	// 拼错的 -p 1LATEST 必须报错，而不是被子串替换成页码 112 静默下载别的分P。
	cfg.SelectPage = "1LATEST"
	if got, err := getSelectedPages(&cfg, vInfo, ""); err == nil {
		t.Errorf("-p 1LATEST 应报错，实际得到 %v", got)
	}
}
