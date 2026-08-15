package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// BangumiInfoFetcher fetches bangumi/anime info (upstream: section fallback,
// 预告 skip, long_title concatenation, publish.pub_time parsing).
type BangumiInfoFetcher struct {
	client *util.HTTPClient
	epHost string
}

func (f *BangumiInfoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	epID := strings.TrimPrefix(id, "ep:")
	epHost := f.epHost
	if epHost == "" {
		epHost = "api.bilibili.com"
	}
	api := fmt.Sprintf("https://%s/pgc/view/web/season?ep_id=%s", epHost, epID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(resp), &root); err != nil {
		return nil, fmt.Errorf("parse bangumi response: %w", err)
	}
	result, ok := root["result"].(map[string]interface{})
	if !ok {
		code := gi(root, "code")
		return nil, fmt.Errorf("获取番剧信息失败 (code=%d): %s", code, gs(root, "message"))
	}

	cover := gs(result, "cover")
	title := gs(result, "title")
	desc := gs(result, "evaluate")
	pubTime := parsePubTime(result)

	var pages []interface{}
	if eps, ok := result["episodes"].([]interface{}); ok {
		pages = eps
	}

	// Episodes empty or not containing the target ep (番外/花絮): search sections.
	foundEp := false
	for _, ep := range pages {
		if em, ok := ep.(map[string]interface{}); ok && gs(em, "id") == epID {
			foundEp = true
			break
		}
	}
	if !foundEp {
		if sections, ok := result["section"].([]interface{}); ok {
			for _, sec := range sections {
				sm, _ := sec.(map[string]interface{})
				if sm == nil {
					continue
				}
				inSection := false
				for _, ep := range ga(sm, "episodes") {
					if em, ok := ep.(map[string]interface{}); ok && gs(em, "id") == epID {
						inSection = true
						break
					}
				}
				if inSection {
					if secTitle := gs(sm, "title"); secTitle != "" {
						title += "[" + secTitle + "]"
					}
					pages = ga(sm, "episodes")
					break
				}
			}
		}
	}

	var pagesInfo []entity.Page
	index := ""
	i := 1
	for _, ep := range pages {
		em, ok := ep.(map[string]interface{})
		if !ok {
			continue
		}
		// Skip trailers (预告).
		if gs(em, "badge") == "预告" {
			continue
		}
		res := dimensionRes(em)
		titleText := gs(em, "title")
		if lt, ok := em["long_title"].(string); ok && lt != "" {
			titleText += " " + lt
		}
		titleText = strings.TrimSpace(titleText)
		p := entity.Page{
			Index:   i,
			Aid:     gs(em, "aid"),
			Cid:     gs(em, "cid"),
			Epid:    gs(em, "id"),
			Title:   titleText,
			Dur:     gi(em, "duration"),
			Res:     res,
			PubTime: gi64(em, "pub_time"),
		}
		i++
		if p.Epid == epID {
			index = fmt.Sprintf("%d", p.Index)
		}
		pagesInfo = append(pagesInfo, p)
	}

	return &entity.VInfo{
		Title:     strings.TrimSpace(title),
		Desc:      strings.TrimSpace(desc),
		Pic:       cover,
		PubTime:   pubTime,
		IsBangumi: true,
		Index:     index,
		PagesInfo: pagesInfo,
	}, nil
}

// parsePubTime reads publish.pub_time (string "yyyy-MM-dd HH:mm:ss") with a
// numeric publish_time fallback.
func parsePubTime(m map[string]interface{}) int64 {
	if pub, ok := m["publish"].(map[string]interface{}); ok {
		if s := gs(pub, "pub_time"); s != "" {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
				return t.Unix()
			}
		}
	}
	return gi64(m, "publish_time")
}

// dimensionRes formats dimension.width x dimension.height.
func dimensionRes(m map[string]interface{}) string {
	dim, ok := m["dimension"].(map[string]interface{})
	if !ok {
		return ""
	}
	w := gi(dim, "width")
	h := gi(dim, "height")
	if w > 0 && h > 0 {
		return fmt.Sprintf("%dx%d", w, h)
	}
	return ""
}
