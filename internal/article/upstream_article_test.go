package article

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// 本文件搬上游 ArticleUtilTests 的两张表：cv 号解析与 Markdown 头部。

func TestUpstreamExtractCvId(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"cv123", "123"},
		{"CV456", "456"},
		{"https://www.bilibili.com/read/cv789", "789"},
	} {
		got, err := ExtractCvId(c.in)
		if err != nil {
			t.Errorf("ExtractCvId(%q) 报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ExtractCvId(%q) = %q，上游期望 %q", c.in, got, c.want)
		}
	}

	// 无法识别时必须报错（上游抛 ArgumentException），不能返回空串继续跑
	for _, bad := range []string{"", "av123", "not-a-cv"} {
		if got, err := ExtractCvId(bad); err == nil {
			t.Errorf("ExtractCvId(%q) = %q，上游要求报错", bad, got)
		}
	}
}

// TestUpstreamArticleMarkdownHeader 钉住头部三行与时间格式（上游断言 发布时间行
// 以 HH:MM 结尾，且标题/作者/正文都在）。

func TestUpstreamArticleMarkdownHeader(t *testing.T) {
	a := &Article{
		Title:   "测试标题",
		Author:  "作者君",
		PubTime: time.Now().Unix(),
		Content: "这是正文。",
	}
	path := filepath.Join(t.TempDir(), "a.md")
	if err := SaveAsMarkdown(a, path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)

	for _, want := range []string{"# 测试标题", "作者: 作者君", "这是正文。"} {
		if !strings.Contains(text, want) {
			t.Errorf("Markdown 缺 %q:\n%s", want, text)
		}
	}

	m := regexp.MustCompile(`(?m)^> 作者: .*  \|  发布时间: .*\d{2}:\d{2}$`).FindString(text)
	if m == "" {
		t.Errorf("发布时间行格式不符（应含作者、发布时间且以 HH:MM 结尾）:\n%s", text)
	}
}
