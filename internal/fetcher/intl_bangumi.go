package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// intlStateRegex extracts window.__INITIAL_STATE__ from a bangumi web page
// (upstream StateRegex).
var intlStateRegex = regexp.MustCompile("window.__INITIAL_STATE__=([\\s\\S].*?);\\(function\\(\\)")

// IntlBangumiInfoFetcher fetches international bangumi info (upstream:
// api.bilibili.tv host selection, bstar_a app, cover fallback via the web
// page, modules section handling, 预告 skip).
type IntlBangumiInfoFetcher struct {
	client *util.HTTPClient
	host   string
	token  string
}

func (f *IntlBangumiInfoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	epID := strings.TrimPrefix(id, "ep:")
	index := ""

	host := f.host
	if host == "" || host == "api.bilibili.com" {
		host = "api.bilibili.tv"
	}
	api := fmt.Sprintf("https://%s/intl/gateway/v2/ogv/view/app/season?ep_id=%s&platform=android&s_locale=zh_SG&mobi_app=bstar_a", host, epID)
	if f.token != "" {
		api += "&access_key=" + f.token
	}
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}
	resp = strings.ReplaceAll(resp, "\\/", "/")

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(resp), &root); err != nil {
		return nil, fmt.Errorf("parse intl bangumi response: %w", err)
	}
	result, ok := root["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("获取国际版番剧信息失败 (code=%d): %s", gi(root, "code"), gs(root, "message"))
	}

	seasonID := gs(result, "season_id")
	cover := gs(result, "cover")
	title := gs(result, "title")
	desc := gs(result, "evaluate")

	// Cover fallback via the web page __INITIAL_STATE__ (upstream).
	if cover == "" {
		animeURL := "https://bangumi.bilibili.com/anime/" + seasonID
		if web, err := f.client.GetWebSource(ctx, animeURL); err == nil {
			if m := intlStateRegex.FindStringSubmatch(web); m != nil {
				var state map[string]interface{}
				if json.Unmarshal([]byte(m[1]), &state) == nil {
					if mediaInfo, ok := state["mediaInfo"].(map[string]interface{}); ok {
						cover = gs(mediaInfo, "cover")
						if t := gs(mediaInfo, "title"); t != "" {
							title = t
						}
						if d := gs(mediaInfo, "evaluate"); d != "" {
							desc = d
						}
					}
				}
			}
		}
	}

	pubTime := parsePubTime(result)

	var pages []interface{}
	if eps, ok := result["episodes"].([]interface{}); ok {
		pages = eps
	}
	// modules: find the module containing the target ep and use its episodes.
	if modules, ok := result["modules"].([]interface{}); ok {
		for _, sec := range modules {
			sm, _ := sec.(map[string]interface{})
			if sm == nil {
				continue
			}
			secData, ok := sm["data"].(map[string]interface{})
			if !ok {
				continue
			}
			foundInSection := false
			for _, ep := range ga(secData, "episodes") {
				if em, ok := ep.(map[string]interface{}); ok && gs(em, "id") == epID {
					foundInSection = true
					break
				}
			}
			if foundInSection {
				pages = ga(secData, "episodes")
				break
			}
		}
	}

	var pagesInfo []entity.Page
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
