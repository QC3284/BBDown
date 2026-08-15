package util

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// CommentItem is one exported comment (upstream CommentUtil).
type CommentItem struct {
	User    string `json:"user"`
	Time    string `json:"time"`
	Likes   int    `json:"likes"`
	Content string `json:"content"`
}

// FetchComments downloads up to maxPages of replies for an aid.
// truncated is true when the last fetched page still had replies.
func FetchComments(ctx context.Context, client *HTTPClient, aid string, maxPages int) ([]CommentItem, bool, error) {
	if maxPages <= 0 {
		maxPages = 20
	}
	var items []CommentItem
	truncated := false
	for pn := 1; pn <= maxPages; pn++ {
		api := fmt.Sprintf("https://api.bilibili.com/x/v2/reply?type=1&oid=%s&sort=0&ps=20&pn=%d", aid, pn)
		source, err := client.GetWebSource(ctx, api)
		if err != nil {
			return nil, false, fmt.Errorf("获取评论失败: %w", err)
		}
		var r struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Replies []struct {
					Member struct {
						Uname string `json:"uname"`
					} `json:"member"`
					Ctime   int `json:"ctime"`
					Like    int `json:"like"`
					Content struct {
						Message string `json:"message"`
					} `json:"content"`
				} `json:"replies"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(source), &r); err != nil {
			return nil, false, fmt.Errorf("解析评论响应失败: %w", err)
		}
		if r.Code != 0 {
			return nil, false, fmt.Errorf("获取评论失败(code=%d): %s", r.Code, r.Message)
		}
		if len(r.Data.Replies) == 0 {
			break
		}
		for _, reply := range r.Data.Replies {
			items = append(items, CommentItem{
				User:    reply.Member.Uname,
				Time:    time.Unix(int64(reply.Ctime), 0).Local().Format("2006-01-02 15:04:05"),
				Likes:   reply.Like,
				Content: strings.TrimSpace(reply.Content.Message),
			})
		}
		if pn == maxPages && len(r.Data.Replies) > 0 {
			truncated = true
		}
	}
	return items, truncated, nil
}

// SaveCommentsJSON writes the comment list as an indented JSON array.
func SaveCommentsJSON(items []CommentItem, path string) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
