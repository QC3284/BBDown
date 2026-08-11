package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// NormalInfoFetcher fetches info for regular videos.
type NormalInfoFetcher struct {
	client *util.HTTPClient
}

var epIDRegex = regexp.MustCompile(`ep(\d+)`)

func (f *NormalInfoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	api := "https://api.bilibili.com/x/web-interface/view?aid=" + id
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Title       string `json:"title"`
			Desc        string `json:"desc"`
			Pic         string `json:"pic"`
			Pubdate     int64  `json:"pubdate"`
			Bvid        string `json:"bvid"`
			Cid         int64  `json:"cid"`
			RedirectURL string `json:"redirect_url"`
			Owner       struct {
				Mid  json.Number `json:"mid"`
				Name string      `json:"name"`
			} `json:"owner"`
			Rights struct {
				IsSteinGate int `json:"is_stein_gate"`
			} `json:"rights"`
			IsUpowerExclusive bool `json:"is_upower_exclusive"`
			IsUpowerPreview   bool `json:"is_upower_preview"`
			IsUpowerPlay      bool `json:"is_upower_play"`
			Pages             []struct {
				Page     int         `json:"page"`
				Part     string      `json:"part"`
				Cid      json.Number `json:"cid"`
				Duration int         `json:"duration"`
				Dimension struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"dimension"`
			} `json:"pages"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse view response: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("获取视频信息失败 (code=%d): %s", result.Code, result.Message)
	}

	data := result.Data
	title := strings.TrimSpace(data.Title)
	desc := strings.TrimSpace(data.Desc)
	ownerMid := string(data.Owner.Mid)
	ownerName := data.Owner.Name
	bangumi := false

	var pages []entity.Page
	for _, p := range data.Pages {
		cid := string(p.Cid)
		res := ""
		if p.Dimension.Width > 0 {
			res = fmt.Sprintf("%dx%d", p.Dimension.Width, p.Dimension.Height)
		}
		page := entity.Page{
			Index:     p.Page,
			Aid:       id,
			Cid:       cid,
			Title:     strings.TrimSpace(p.Part),
			Dur:       p.Duration,
			Res:       res,
			PubTime:   data.Pubdate,
			OwnerName: ownerName,
			OwnerMid:  ownerMid,
		}
		pages = append(pages, page)
	}

	isSteinGate := data.Rights.IsSteinGate == 1

	// Handle interactive video (SteinGate)
	if isSteinGate && data.Bvid != "" && data.Cid > 0 {
		extraPages, err := f.fetchInteractionPages(ctx, data.Bvid, data.Cid, id, ownerName, ownerMid, data.Pubdate)
		if err == nil {
			pages = append(pages, extraPages...)
		}
	}

	// Check if redirect goes to bangumi
	if data.RedirectURL != "" && strings.Contains(data.RedirectURL, "bangumi") {
		bangumi = true
		match := epIDRegex.FindStringSubmatch(data.RedirectURL)
		if match != nil && len(pages) == 1 {
			for i := range pages {
				pages[i].Epid = match[1]
			}
		}
	}

	return &entity.VInfo{
		Title:             title,
		Desc:              desc,
		Pic:               data.Pic,
		PubTime:           data.Pubdate,
		PagesInfo:         pages,
		IsBangumi:         bangumi,
		IsSteinGate:       isSteinGate,
		IsUpowerExclusive: data.IsUpowerExclusive,
		IsUpowerPreview:   data.IsUpowerPreview,
		IsUpowerPlay:      data.IsUpowerPlay,
	}, nil
}

func (f *NormalInfoFetcher) fetchInteractionPages(ctx context.Context, bvid string, cid int64, aid, ownerName, ownerMid string, pubTime int64) ([]entity.Page, error) {
	playerAPI := fmt.Sprintf("https://api.bilibili.com/x/player.so?bvid=%s&id=cid:%d", bvid, cid)
	resp, err := f.client.GetWebSource(ctx, playerAPI)
	if err != nil {
		return nil, err
	}

	if !strings.Contains(resp, "<interaction>") {
		return nil, fmt.Errorf("no interaction data")
	}

	// Simple extraction of interaction JSON
	start := strings.Index(resp, "<interaction>")
	end := strings.Index(resp, "</interaction>")
	if start < 0 || end < 0 {
		return nil, fmt.Errorf("failed to parse interaction data")
	}
	interactionJSON := resp[start+len("<interaction>"): end]

	var graph struct {
		GraphVersion int64 `json:"graph_version"`
	}
	if err := json.Unmarshal([]byte(interactionJSON), &graph); err != nil {
		return nil, err
	}

	edgeAPI := fmt.Sprintf("https://api.bilibili.com/x/stein/edgeinfo_v2?graph_version=%d&bvid=%s", graph.GraphVersion, bvid)
	edgeResp, err := f.client.GetWebSource(ctx, edgeAPI)
	if err != nil {
		return nil, err
	}

	var edgeData struct {
		Code int `json:"code"`
		Data struct {
			Edges struct {
				Questions []struct {
					Choices []struct {
						Cid    json.Number `json:"cid"`
						Option string      `json:"option"`
					} `json:"choices"`
				} `json:"questions"`
			} `json:"edges"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(edgeResp), &edgeData); err != nil {
		return nil, err
	}

	var pages []entity.Page
	idx := 2 // Interactive video page index starts at 2
	for _, q := range edgeData.Data.Edges.Questions {
		for _, ch := range q.Choices {
			p := entity.Page{
				Index:     idx,
				Aid:       aid,
				Cid:       string(ch.Cid),
				Title:     strings.TrimSpace(ch.Option),
				PubTime:   pubTime,
				OwnerName: ownerName,
				OwnerMid:  ownerMid,
			}
			pages = append(pages, p)
			idx++
		}
	}

	return pages, nil
}

// ParseAid extracts a numeric aid from various formats.
func ParseAid(input string) string {
	// Already numeric
	if _, err := strconv.ParseInt(input, 10, 64); err == nil {
		return input
	}

	// Try BV format
	if strings.HasPrefix(strings.ToUpper(input), "BV") && len(input) >= 12 {
		bvSuffix := input[3:12]
		aid, err := util.BvToAv(bvSuffix)
		if err == nil {
			return strconv.FormatInt(aid, 10)
		}
	}

	return input
}
