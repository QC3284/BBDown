package util

import (
	"math"
	"strings"
	"testing"
)

// 本文件搬上游 SubtitleFormatTests 的两张表：SRT 时间轴与正文净化。
// 时间轴用 hh 会丢掉超过 24 小时的天数；正文里的空行会被 SRT 解析器当作块分隔，
// 令其后所有字幕错位。

func TestUpstreamSubtitleFormatTime(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "00:00:00,000"},
		{64.13, "00:01:04,130"},
		{3661, "01:01:01,000"},
		{86399, "23:59:59,000"},
		// 超过 24 小时必须保留天数折算的小时数（hh 格式符会退化）
		{86400, "24:00:00,000"},
		{90000, "25:00:00,000"},
		{360000, "100:00:00,000"},
		// 毫秒四舍五入而非截断：1.001 秒的差值算出来是 0.000999…
		{1.001, "00:00:01,001"},
	}
	for _, c := range cases {
		if got := FormatSubTime(c.sec); got != c.want {
			t.Errorf("FormatSubTime(%v) = %q，上游期望 %q", c.sec, got, c.want)
		}
	}

	// 负数与 NaN 一律归零（符号不能被静默丢弃）
	for _, bad := range []float64{-1, -3600, math.NaN()} {
		if got := FormatSubTime(bad); got != "00:00:00,000" {
			t.Errorf("FormatSubTime(%v) = %q，上游期望 00:00:00,000", bad, got)
		}
	}

	// 极大值不得溢出/崩溃（上游先夹紧到 TimeSpan.MaxValue 再格式化）
	if got := FormatSubTime(math.MaxFloat64); !strings.HasSuffix(got, ",477") {
		t.Errorf("极大值应夹到 TimeSpan.MaxValue 量级，实际 %q", got)
	}
}

func TestUpstreamSanitizeSrtContent(t *testing.T) {
	// 空行会截断字幕块：必须折叠掉，但合法的多行正文不能压成单行
	if got := SanitizeSRT("第一行\n\n第二行"); got != "第一行\n第二行" {
		t.Errorf("空行未折叠: %q", got)
	}
	if got := SanitizeSRT("上句\n下句"); got != "上句\n下句" {
		t.Errorf("合法多行被改动: %q", got)
	}

	// 行尾空白裁掉、行首空白保留；CRLF/CR 归一到 LF
	if got := SanitizeSRT("行一\r\n行二"); got != "行一\n行二" {
		t.Errorf("CRLF 未归一: %q", got)
	}
	if got := SanitizeSRT("行一\r行二"); got != "行一\n行二" {
		t.Errorf("CR 未归一: %q", got)
	}
	if got := SanitizeSRT("行一   \n   行二"); got != "行一\n   行二" {
		t.Errorf("行尾/行首空白处理不符: %q", got)
	}

	// 全空白 → 空串
	for _, in := range []string{"", "\n", "   \n  \n "} {
		if got := SanitizeSRT(in); got != "" {
			t.Errorf("SanitizeSRT(%q) = %q，上游期望空串", in, got)
		}
	}

	// 正文里的 --> 会被 SRT 解析器当成时间分隔而错位
	if got := SanitizeSRT("a --> b"); strings.Contains(got, "-->") {
		t.Errorf("--> 未中和: %q", got)
	}
}
