package workflow

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/QC3284/BBDown-Go/internal/config"
	"github.com/QC3284/BBDown-Go/internal/entity"
)

func TestParsePageSelection(t *testing.T) {
	cases := []struct {
		expr    string
		want    []string
		wantErr bool
	}{
		{"1,3,5", []string{"1", "3", "5"}, false},
		{"1-3", []string{"1", "2", "3"}, false},
		{"1-3,7,9-11", []string{"1", "2", "3", "7", "9", "10", "11"}, false},
		{"5", []string{"5"}, false},
		{"10-1", nil, true}, // start > end must abort (upstream)
		{"1-3-5", nil, true},
		{"abc", nil, true},
		{"-5", nil, true}, // invalid: clear error instead of upstream token quirk
		{"1,,2", []string{"1", "2"}, false},
	}
	for _, c := range cases {
		got, err := parsePageSelection(c.expr)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePageSelection(%q) expected error, got %v", c.expr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePageSelection(%q) unexpected error: %v", c.expr, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePageSelection(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestParsePageSelectionMaxExpanded(t *testing.T) {
	_, err := parsePageSelection("1-1000001")
	if err == nil {
		t.Fatal("expected expansion limit error")
	}
}

func TestParseEncodingPriority(t *testing.T) {
	m, first := parseEncodingPriority("hevc,avc,av1")
	if first != "HEVC" {
		t.Errorf("first = %q, want HEVC", first)
	}
	if m["HEVC"] != 0 || m["AVC"] != 1 || m["AV1"] != 2 {
		t.Errorf("map = %v", m)
	}
	// Earlier = higher priority: HEVC(0) < AVC(1).

	// Chinese comma, dashes removed (dots kept, matching upstream), dedup.
	m, first = parseEncodingPriority("H.264，hevc,-av1-,hevc")
	if first != "H.264" {
		t.Errorf("first = %q, want H.264", first)
	}
	if m["H.264"] != 0 || m["HEVC"] != 1 || m["AV1"] != 2 {
		t.Errorf("map = %v", m)
	}
}

func TestParseDfnPriority(t *testing.T) {
	m := parseDfnPriority("8K, 1080P 高码率，720P 高清")
	if m["8K"] != 0 || m["1080P 高码率"] != 1 || m["720P 高清"] != 2 {
		t.Errorf("map = %v", m)
	}
}

func TestParseDanmakuFormats(t *testing.T) {
	got, err := parseDanmakuFormats("")
	if err != nil || !reflect.DeepEqual(got, []string{"xml", "ass"}) {
		t.Errorf("default = %v, %v", got, err)
	}
	got, _ = parseDanmakuFormats("XML，ass")
	if !reflect.DeepEqual(got, []string{"xml", "ass"}) {
		t.Errorf("formats = %v", got)
	}
	got, _ = parseDanmakuFormats("bogus")
	if !reflect.DeepEqual(got, []string{"xml", "ass"}) {
		t.Errorf("invalid format should fall back to defaults, got %v", got)
	}
}

// ---- t66：分P选择的前导零规范化（吸收上游 c39cae4）+ 展示截断 ----

// TestParsePageSelectionNormalizesLeadingZeros 钉住 t66 的核心修复：
// 单 token 分支必须输出**规范形态**（去掉前导零），否则 "-p 01" 会被原样塞进选择列表，
// 而真实分P 是 "1"——字符串比对匹配不上，用户**静默少下**（不报错、不提示）。
// 范围与混合表达式本来就由 strconv.Itoa 产出，这里一并逐字钉住三条路径同口径。
//
// 变异验证：把单 token 分支改回 append(result, part) → 本用例红。
func TestParsePageSelectionNormalizesLeadingZeros(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{"01", []string{"1"}},
		{"001", []string{"1"}},
		{"007", []string{"7"}},
		{"01,2", []string{"1", "2"}},
		{"01-03", []string{"1", "2", "3"}},
		{"01-03,5", []string{"1", "2", "3", "5"}},
		{" 01 , 02 ", []string{"1", "2"}},
		{"1,03,1-2", []string{"1", "3", "1", "2"}},
	}
	for _, c := range cases {
		got, err := parsePageSelection(c.expr)
		if err != nil {
			t.Errorf("parsePageSelection(%q) 不该报错：%v", c.expr, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePageSelection(%q) = %v, want %v", c.expr, got, c.want)
		}
		for _, one := range got {
			if len(one) > 1 && one[0] == '0' {
				t.Errorf("选择项 %q 仍带前导零（会与真实分P 匹配不上）", one)
			}
		}
	}

	// 全零/负数等非法输入的错误语义不变。
	for _, bad := range []string{"0", "00", "-5", "abc"} {
		if _, err := parsePageSelection(bad); err == nil {
			t.Errorf("parsePageSelection(%q) 应当报错", bad)
		}
	}
}

// TestFilterPagesBySelectionMatchesNumerically 钉住三处选择来源的同一收口：
// -p（已规范）、URL 的 ?p=01、以及 VInfo 自带的 "01" 都要能匹配到 Index=1 那一页。
//
// 变异验证：把匹配改回字符串相等 → ?p=01 那一格红。
func TestFilterPagesBySelectionMatchesNumerically(t *testing.T) {
	pages := []entity.Page{{Index: 1}, {Index: 2}, {Index: 3}, {Index: 10}}
	cases := []struct {
		name string
		sel  []string
		want []int
	}{
		{"规范值", []string{"1", "3"}, []int{1, 3}},
		{"前导零（?p=01 / VInfo.Index）", []string{"01"}, []int{1}},
		{"混合前导零", []string{"02", "010"}, []int{2, 10}},
		{"首尾空白", []string{" 3 "}, []int{3}},
		{"不存在的页", []string{"9"}, nil},
		{"非数字项退回字符串比对", []string{"x"}, nil},
	}
	for _, c := range cases {
		got := filterPagesBySelection(pages, c.sel)
		var idx []int
		for _, p := range got {
			idx = append(idx, p.Index)
		}
		if len(idx) != len(c.want) {
			t.Errorf("%s：filterPagesBySelection(%v) = %v, want %v", c.name, c.sel, idx, c.want)
			continue
		}
		for i := range c.want {
			if idx[i] != c.want[i] {
				t.Errorf("%s：filterPagesBySelection(%v) = %v, want %v", c.name, c.sel, idx, c.want)
			}
		}
	}
	// 过滤结果保持原顺序（下载顺序 = 分P 顺序）。
	if got := filterPagesBySelection(pages, []string{"03", "01"}); len(got) != 2 || got[0].Index != 1 || got[1].Index != 3 {
		t.Errorf("过滤结果应当保持分P 原顺序：%+v", got)
	}
	// 空结果必须是空切片而不是 nil（调用方靠 len 判断，别引入 nil 语义差异）。
	if got := filterPagesBySelection(pages, []string{"99"}); got == nil {
		t.Error("空结果应当是空切片")
	}
}

// TestFormatSelectedPagesTruncatesLongSelections 钉住展示截断（上游同修）：
// 像 -p 1-60000 那样的大选择，日志/错误里只展示前 20 项 + 总数——否则一条几十 KB 的日志行
// 会把终端与 issue 一起刷爆。
//
// 变异验证：撤掉截断（直接 Join 全部）→ 本用例红。
func TestFormatSelectedPagesTruncatesLongSelections(t *testing.T) {
	if got := formatSelectedPages(nil); got != "ALL" {
		t.Errorf("nil（= ALL）应当显示 ALL，实际 %q", got)
	}
	if got := formatSelectedPages([]string{"1", "2"}); got != "1,2" {
		t.Errorf("少量选择应当原样列出，实际 %q", got)
	}
	// 恰好 20 项：不截断。
	exact := make([]string, maxShownPages)
	for i := range exact {
		exact[i] = strconv.Itoa(i + 1)
	}
	got := formatSelectedPages(exact)
	if strings.Contains(got, "共") || !strings.HasPrefix(got, "1,2,") {
		t.Errorf("恰好 %d 项不该截断：%q", maxShownPages, got)
	}
	// 21 项与 1000 项：前 20 项 + 总数。
	for _, n := range []int{maxShownPages + 1, 1000} {
		all := make([]string, n)
		for i := range all {
			all[i] = strconv.Itoa(i + 1)
		}
		got := formatSelectedPages(all)
		wantPrefix := all[:maxShownPages][maxShownPages-1] + "…"
		_ = wantPrefix
		if !strings.HasSuffix(got, fmt.Sprintf("…（共 %d 项）", n)) {
			t.Errorf("%d 项应当带总数后缀：%q", n, got)
		}
		if strings.Count(got, ",") != maxShownPages-1 {
			t.Errorf("%d 项应当只展示前 %d 项：%q", n, maxShownPages, got)
		}
		if want := strings.Join(all[:maxShownPages], ","); !strings.HasPrefix(got, want) {
			t.Errorf("%d 项的前缀应当是前 %d 项：%q", n, maxShownPages, got)
		}
	}
}

// TestGetSelectedPagesNormalizesLeadingZeros 端到端：-p 01 经 getSelectedPages 出来就是 ["1"]，
// 于是「已选择：1」与实际分P "1" 能对上（改前这里会静默少下）。
func TestGetSelectedPagesNormalizesLeadingZeros(t *testing.T) {
	cfg := &config.MyOption{SelectPage: "01"}
	vInfo := &entity.VInfo{PagesInfo: []entity.Page{{Index: 1}, {Index: 2}}}
	got, err := getSelectedPages(cfg, vInfo, "")
	if err != nil {
		t.Fatalf("getSelectedPages: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("getSelectedPages(-p 01) = %v, want [1]", got)
	}
	// 没给选择、也没有自动选择来源时是 ALL（nil）。
	cfg2 := &config.MyOption{}
	got2, err := getSelectedPages(cfg2, &entity.VInfo{}, "")
	if err != nil || got2 != nil {
		t.Errorf("未指定分P 应当是 ALL(nil)，实际 %v err=%v", got2, err)
	}
}
