package util

import (
	"strings"
	"testing"
)

// 本文件把上游 BBDown.Tests/FormatHelperTests.cs 的数据表搬过来做差分：
// 上游测试即格式规格，逐条跑一遍即可撞出与上游不一致的输出。

func TestUpstreamFormatFileSize(t *testing.T) {
	cases := []struct {
		size float64
		want string
	}{
		{0, "0 bytes"},
		{512, "512 bytes"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{2.5 * 1024 * 1024 * 1024, "2.50 GB"},
	}
	for _, c := range cases {
		if got := FormatFileSize(c.size); got != c.want {
			t.Errorf("FormatFileSize(%v) = %q, 上游期望 %q", c.size, got, c.want)
		}
	}
}

func TestUpstreamFormatTime(t *testing.T) {
	cases := []struct {
		seconds  int
		absolute bool
		want     string
	}{
		{0, false, "00m00s"},
		{65, false, "01m05s"},
		{3661, false, "1h01m01s"},
		{3661, true, "01:01:01"},
	}
	for _, c := range cases {
		if got := FormatTime(c.seconds, c.absolute); got != c.want {
			t.Errorf("FormatTime(%d, %v) = %q, 上游期望 %q", c.seconds, c.absolute, got, c.want)
		}
	}
}

// TestUpstreamGetValidFileName 搬上游 PathUtilTests 的三张表。
func TestUpstreamGetValidFileName(t *testing.T) {
	short := []struct{ in, want string }{
		{"正常标题", "正常标题"},
		{`a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"CON", "_CON"},
		{"con.txt", "_con.txt"},
		{"NUL.foo", "_NUL.foo"},
	}
	for _, c := range short {
		if got := GetValidFileName(c.in, "_", false); got != c.want {
			t.Errorf("GetValidFileName(%q) = %q, 上游期望 %q", c.in, got, c.want)
		}
	}

	trailing := []struct{ in, want string }{
		{"video.", "video"},
		{"video ", "video"},
		{"video.. ", "video"},
		{".", "_"},
		{"..", "_"},
		{"...", "_"},
		{" ", "_"},
		{" . ", "_"},
	}
	for _, c := range trailing {
		if got := GetValidFileName(c.in, "_", false); got != c.want {
			t.Errorf("GetValidFileName(%q) = %q, 上游期望 %q", c.in, got, c.want)
		}
	}

	// 上游 GetValidFileName_TruncatesLongBaseName_AndKeepsExtension：超长时保留扩展名
	long := strings.Repeat("a", 200) + ".mp4"
	got := GetValidFileName(long, "_", false)
	if !strings.HasSuffix(got, ".mp4") {
		t.Errorf("超长基名截断后丢了扩展名: %q", got)
	}
	if len(got) > 100 {
		t.Errorf("截断后长度 %d，上游上限 100: %q", len(got), got)
	}
}
