package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// FavListFetcher fetches a favorite list.
type FavListFetcher struct {
	client *util.HTTPClient
}

func (f *FavListFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	favID := strings.TrimPrefix(id, "favId:")
	api := fmt.Sprintf("https://api.bilibili.com/x/v3/fav/resource/list?media_id=%s&ps=20&pn=1", favID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Info struct {
				Title string `json:"title"`
				Cover string `json:"cover"`
			} `json:"info"`
			Medias []struct {
				ID       int64       `json:"id"`
				Aid      json.Number `json:"aid"`
				Cid      json.Number `json:"cid"`
				Title    string      `json:"title"`
				Cover    string      `json:"cover"`
				Duration int         `json:"duration"`
				Upper    struct {
					Mid  json.Number `json:"mid"`
					Name string      `json:"name"`
				} `json:"upper"`
			} `json:"medias"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse favlist response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("获取收藏夹信息失败 (code=%d): %s", result.Code, result.Message)
	}

	d := result.Data
	var pages []entity.Page
	for i, m := range d.Medias {
		p := entity.Page{
			Index:     i + 1,
			Aid:       string(m.Aid),
			Cid:       string(m.Cid),
			Title:     m.Title,
			Dur:       m.Duration,
			Cover:     m.Cover,
			OwnerName: m.Upper.Name,
			OwnerMid:  string(m.Upper.Mid),
		}
		pages = append(pages, p)
	}

	return &entity.VInfo{
		Title:     d.Info.Title,
		Pic:       d.Info.Cover,
		PagesInfo: pages,
	}, nil
}
