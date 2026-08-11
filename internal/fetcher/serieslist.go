package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// SeriesListFetcher fetches a series list.
type SeriesListFetcher struct {
	client *util.HTTPClient
}

func (f *SeriesListFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	bizID := strings.TrimPrefix(id, "seriesBizId:")
	api := fmt.Sprintf("https://api.bilibili.com/x/v1/series/info?series_id=%s", bizID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Meta struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Cover       string `json:"cover"`
			} `json:"meta"`
			Archives []struct {
				Aid      json.Number `json:"aid"`
				Cid      json.Number `json:"cid"`
				Title    string      `json:"title"`
				Cover    string      `json:"cover"`
				Duration int         `json:"duration"`
				Owner    struct {
					Mid  json.Number `json:"mid"`
					Name string      `json:"name"`
				} `json:"owner"`
			} `json:"archives"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse series response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("获取系列信息失败 (code=%d): %s", result.Code, result.Message)
	}

	d := result.Data
	var pages []entity.Page
	for i, a := range d.Archives {
		p := entity.Page{
			Index:     i + 1,
			Aid:       string(a.Aid),
			Cid:       string(a.Cid),
			Title:     a.Title,
			Dur:       a.Duration,
			Cover:     a.Cover,
			OwnerName: a.Owner.Name,
			OwnerMid:  string(a.Owner.Mid),
		}
		pages = append(pages, p)
	}

	return &entity.VInfo{
		Title:     d.Meta.Name,
		Desc:      d.Meta.Description,
		Pic:       d.Meta.Cover,
		PagesInfo: pages,
	}, nil
}
