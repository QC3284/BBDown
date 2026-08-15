package article

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// Article is a fetched Bilibili column article.
type Article struct {
	Title   string
	Author  string
	PubTime int64
	Content string
}

var cvRegex = regexp.MustCompile(`cv(\d+)`)

// ExtractCvId extracts the numeric cv id from "cv123" or a read URL.
func ExtractCvId(input string) (string, error) {
	m := cvRegex.FindStringSubmatch(strings.ToLower(input))
	if m == nil {
		return "", fmt.Errorf("输入有误：无法识别的专栏 ID，当前值: %q", input)
	}
	return m[1], nil
}

// Fetch downloads the article via the view API (upstream ArticleUtil.FetchAsync).
func Fetch(ctx context.Context, client *util.HTTPClient, cvID string) (*Article, error) {
	api := "https://api.bilibili.com/x/article/view?id=" + cvID
	source, err := client.GetWebSource(ctx, api)
	if err != nil {
		return nil, fmt.Errorf("专栏获取失败: %w", err)
	}
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Title       string `json:"title"`
			AuthorName  string `json:"author_name"`
			PublishTime int64  `json:"publish_time"`
			Content     string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(source), &r); err != nil {
		return nil, fmt.Errorf("解析专栏响应失败: %w", err)
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("专栏获取失败(code=%d): %s", r.Code, r.Message)
	}
	title := strings.TrimSpace(r.Data.Title)
	if title == "" {
		title = "专栏" + cvID
	}
	content := r.Data.Content
	if content == "" {
		content = "（正文为空，可能为会员专享或已删除）"
	}
	return &Article{
		Title:   title,
		Author:  r.Data.AuthorName,
		PubTime: r.Data.PublishTime,
		Content: content,
	}, nil
}

// SaveAsMarkdown writes the article as Markdown (upstream: the content field
// is already Markdown and is passed through verbatim; no image download).
func SaveAsMarkdown(a *Article, path string) error {
	var sb strings.Builder
	sb.WriteString("# " + a.Title + "\n")
	sb.WriteString("\n")
	t := time.Unix(a.PubTime, 0).Local()
	sb.WriteString(fmt.Sprintf("> 作者: %s  |  发布时间: %s\n", a.Author, t.Format("2006-01-02 15:04")))
	sb.WriteString("\n")
	sb.WriteString(a.Content + "\n")
	sb.WriteString("\n")
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
