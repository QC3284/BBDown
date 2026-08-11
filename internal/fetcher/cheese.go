package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// CheeseInfoFetcher fetches cheese/course info.
type CheeseInfoFetcher struct {
	client *util.HTTPClient
}

func (f *CheeseInfoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	epID := strings.TrimPrefix(id, "cheese:")
	api := fmt.Sprintf("https://api.bilibili.com/pugv/view/web/season?ep_id=%s", epID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Title       string `json:"title"`
			Subtitle    string `json:"subtitle"`
			Cover       string `json:"cover"`
			PublishTime int64  `json:"publish_time"`
			Episodes    []struct {
				ID        int64       `json:"id"`
				Aid       json.Number `json:"aid"`
				Cid       json.Number `json:"cid"`
				Title     string      `json:"title"`
				Index     int         `json:"index"`
				Duration  int         `json:"duration"`
				Dimension struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"dimension"`
			} `json:"episodes"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse cheese response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("获取课程信息失败 (code=%d): %s", result.Code, result.Message)
	}

	d := result.Data
	var pages []entity.Page
	var index string

	for _, ep := range d.Episodes {
		cid := string(ep.Cid)
		aid := string(ep.Aid)
		res := ""
		if ep.Dimension.Width > 0 {
			res = fmt.Sprintf("%dx%d", ep.Dimension.Width, ep.Dimension.Height)
		}

		p := entity.Page{
			Index:   ep.Index,
			Aid:     aid,
			Cid:     cid,
			Epid:    fmt.Sprintf("%d", ep.ID),
			Title:   ep.Title,
			Dur:     ep.Duration,
			Res:     res,
			PubTime: d.PublishTime,
		}
		pages = append(pages, p)

		if fmt.Sprintf("%d", ep.ID) == epID {
			index = fmt.Sprintf("%d", ep.Index)
		}
	}

	return &entity.VInfo{
		Title:     d.Title,
		Desc:      d.Subtitle,
		Pic:       d.Cover,
		PubTime:   d.PublishTime,
		IsCheese:  true,
		Index:     index,
		PagesInfo: pages,
	}, nil
}
