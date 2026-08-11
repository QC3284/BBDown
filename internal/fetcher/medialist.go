package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// MediaListFetcher fetches a media list (collection).
type MediaListFetcher struct {
	client *util.HTTPClient
}

func (f *MediaListFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	bizID := strings.TrimPrefix(id, "listBizId:")
	api := fmt.Sprintf("https://api.bilibili.com/x/v1/medialist/info?type=8&biz_id=%s", bizID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Title   string `json:"title"`
			Intro   string `json:"intro"`
			Cover   string `json:"cover"`
			MediaList []struct {
				ID       int64       `json:"id"`
				Aid      json.Number `json:"aid"`
				Cid      json.Number `json:"cid"`
				Title    string      `json:"title"`
				Cover    string      `json:"cover"`
				Duration int         `json:"duration"`
				Owner    struct {
					Mid  json.Number `json:"mid"`
					Name string      `json:"name"`
				} `json:"owner"`
			} `json:"medias"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse medialist response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("获取合集信息失败 (code=%d): %s", result.Code, result.Message)
	}

	d := result.Data
	var pages []entity.Page
	for i, m := range d.MediaList {
		p := entity.Page{
			Index:     i + 1,
			Aid:       string(m.Aid),
			Cid:       string(m.Cid),
			Title:     m.Title,
			Dur:       m.Duration,
			Cover:     m.Cover,
			OwnerName: m.Owner.Name,
			OwnerMid:  string(m.Owner.Mid),
		}
		pages = append(pages, p)
	}

	return &entity.VInfo{
		Title:     d.Title,
		Desc:      d.Intro,
		Pic:       d.Cover,
		PagesInfo: pages,
	}, nil
}
