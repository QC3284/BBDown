package article

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractCvId(t *testing.T) {
	cases := map[string]string{
		"cv123":                               "123",
		"https://www.bilibili.com/read/cv123": "123",
		"CV170001":                            "170001",
	}
	for in, want := range cases {
		got, err := ExtractCvId(in)
		if err != nil || got != want {
			t.Errorf("ExtractCvId(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ExtractCvId("not-an-article"); err == nil {
		t.Error("invalid input should error")
	}
}

func TestSaveAsMarkdown(t *testing.T) {
	a := &Article{
		Title:   "测试专栏",
		Author:  "作者A",
		PubTime: 1500000000,
		Content: "正文内容\n\n第二段",
	}
	out := filepath.Join(t.TempDir(), "out.md")
	if err := SaveAsMarkdown(a, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "# 测试专栏\n\n") {
		t.Errorf("missing title header: %q", text)
	}
	if !strings.Contains(text, "> 作者: 作者A") {
		t.Errorf("missing author line: %q", text)
	}
	if !strings.HasSuffix(text, "正文内容\n\n第二段\n\n") {
		t.Errorf("missing content passthrough: %q", text)
	}
}
