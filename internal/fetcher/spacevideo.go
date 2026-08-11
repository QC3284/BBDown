package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// SpaceVideoFetcher fetches all videos from an uploader's space.
type SpaceVideoFetcher struct {
	client *util.HTTPClient
}

func (f *SpaceVideoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	mid := strings.TrimPrefix(id, "mid:")
	// Simplified: fetches the first page of the uploader's video list.
	// A full implementation would use VInfo to wrap multiple regular pages.
	api := fmt.Sprintf("https://api.bilibili.com/x/space/wbi/arc/search?mid=%s&ps=50&pn=1", mid)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			List struct {
				VList []struct {
					Aid       json.Number `json:"aid"`
					Title     string      `json:"title"`
					Author    string      `json:"author"`
					Mid       json.Number `json:"mid"`
					Pic       string      `json:"pic"`
					Duration  string      `json:"duration"`
					Created   int64       `json:"created"`
					Videos    int         `json:"videos"`
				} `json:"vlist"`
			} `json:"list"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse space video response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("获取UP主投稿信息失败 (code=%d): %s", result.Code, result.Message)
	}

	var pages []entity.Page
	for _, v := range result.Data.List.VList {
		aid := string(v.Aid)
		dur := parseDuration(v.Duration)
		p := entity.Page{
			Index:     v.Videos, // Use video count as index (placeholder)
			Aid:       aid,
			Cid:       "", // Will be resolved later
			Title:     v.Title,
			Dur:       dur,
			PubTime:   v.Created,
			Cover:     v.Pic,
			OwnerName: v.Author,
			OwnerMid:  string(v.Mid),
		}
		pages = append(pages, p)
	}

	return &entity.VInfo{
		Title:     fmt.Sprintf("UP主 %s 的全部投稿", mid),
		PagesInfo: pages,
	}, nil
}

func parseDuration(d string) int {
	// Format: "MM:SS" or "HH:MM:SS"
	parts := strings.Split(d, ":")
	if len(parts) == 2 {
		var m, s int
		fmt.Sscanf(d, "%d:%d", &m, &s)
		return m*60 + s
	}
	if len(parts) == 3 {
		var h, m, s int
		fmt.Sscanf(d, "%d:%d:%d", &h, &m, &s)
		return h*3600 + m*60 + s
	}
	return 0
}
