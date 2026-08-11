package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// IntlBangumiInfoFetcher fetches international bangumi info.
type IntlBangumiInfoFetcher struct {
	client *util.HTTPClient
}

func (f *IntlBangumiInfoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	epID := strings.TrimPrefix(id, "ep:")
	api := fmt.Sprintf("https://api.biliintl.com/intl/gateway/v2/ogv/view/app/season?ep_id=%s&platform=android&s_locale=zh_SG", epID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			Title       string `json:"title"`
			Evaluate    string `json:"evaluate"`
			Cover       string `json:"cover"`
			PublishTime int64  `json:"publish_time"`
			Episodes    []struct {
				ID        int64       `json:"episode_id"`
				Aid       json.Number `json:"aid"`
				Cid       json.Number `json:"cid"`
				Title     string      `json:"title"`
				LongTitle string      `json:"long_title"`
				Cover     string      `json:"cover"`
				Dimension struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"dimension"`
				Duration int `json:"duration"`
			} `json:"episodes"`
			Badge string `json:"badge"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse intl bangumi response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("获取国际版番剧信息失败 (code=%d): %s", result.Code, result.Message)
	}

	r := result.Result
	isEnd := strings.Contains(r.Badge, "完结")

	var pages []entity.Page
	var index string
	for i, ep := range r.Episodes {
		cid := string(ep.Cid)
		aid := string(ep.Aid)
		res := ""
		if ep.Dimension.Width > 0 {
			res = fmt.Sprintf("%dx%d", ep.Dimension.Width, ep.Dimension.Height)
		}

		p := entity.Page{
			Index:   i + 1,
			Aid:     aid,
			Cid:     cid,
			Epid:    fmt.Sprintf("%d", ep.ID),
			Title:   ep.LongTitle,
			Dur:     ep.Duration,
			Res:     res,
			Cover:   ep.Cover,
			PubTime: r.PublishTime,
		}
		if p.Title == "" {
			p.Title = ep.Title
		}
		pages = append(pages, p)

		if fmt.Sprintf("%d", ep.ID) == epID {
			index = fmt.Sprintf("%d", i+1)
		}
	}

	return &entity.VInfo{
		Title:       r.Title,
		Desc:        r.Evaluate,
		Pic:         r.Cover,
		PubTime:     r.PublishTime,
		IsBangumi:   true,
		IsBangumiEnd: isEnd,
		Index:       index,
		PagesInfo:   pages,
	}, nil
}
