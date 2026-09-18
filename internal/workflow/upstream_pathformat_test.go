package workflow

import "testing"

// 上游 PathFormatTests.ResolveSavePathFormat_SelectsTemplateBasedOnActualPageCount 的整张表。
func TestUpstreamResolveSavePathFormat(t *testing.T) {
	cases := []struct {
		filePattern      string
		multiFilePattern string
		pageCount        int
		forceMulti       bool
		want             string
	}{
		{"", "", 1, false, "<videoTitle>"},
		{"", "", 3, false, "<videoTitle>/[P<pageNumberWithZero>]<pageTitle>"},
		{"", "", 1, true, "<videoTitle>/[P<pageNumberWithZero>]<pageTitle>"},
		{"<Fpat>", "", 1, false, "<Fpat>"},
		{"", "<Mpat>", 3, false, "<Mpat>"},
		{"<Fpat>", "<Mpat>", 1, true, "<Mpat>"},
		{"<Fpat>", "<Mpat>", 1, false, "<Fpat>"},
	}
	for _, c := range cases {
		got := resolveSavePathFormat(c.filePattern, c.multiFilePattern, c.pageCount, c.forceMulti)
		if got != c.want {
			t.Errorf("resolveSavePathFormat(%q, %q, %d, %v) = %q, 上游期望 %q",
				c.filePattern, c.multiFilePattern, c.pageCount, c.forceMulti, got, c.want)
		}
	}
}
