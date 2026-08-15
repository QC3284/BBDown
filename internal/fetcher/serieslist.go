package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// SeriesListFetcher fetches a series list (列表, upstream: medialist info
// type=5 + paginated v2 resource list).
type SeriesListFetcher struct {
	client *util.HTTPClient
}

func (f *SeriesListFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	bizID := strings.TrimPrefix(id, "seriesBizId:")
	api := fmt.Sprintf("https://api.bilibili.com/x/v1/medialist/info?type=5&biz_id=%s&tid=0", bizID)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(resp), &root); err != nil {
		return nil, err
	}
	data := gm(root, "data")
	if data == nil {
		return nil, fmt.Errorf("获取系列信息失败(code=%d): %s", gi(root, "code"), gs(root, "message"))
	}

	listTitle := gs(data, "title")
	intro := gs(data, "intro")
	pubTime := gi64(data, "ctime")

	pagesInfo, err := f.fetchSeriesPages(ctx, bizID)
	if err != nil {
		return nil, err
	}

	return &entity.VInfo{
		Title:     strings.TrimSpace(listTitle),
		Desc:      strings.TrimSpace(intro),
		PubTime:   pubTime,
		PagesInfo: pagesInfo,
		IsBangumi: false,
	}, nil
}

func (f *SeriesListFetcher) fetchSeriesPages(ctx context.Context, bizID string) ([]entity.Page, error) {
	var pagesInfo []entity.Page
	hasMore := true
	oid := ""
	index := 1
	for hasMore {
		listAPI := fmt.Sprintf("https://api.bilibili.com/x/v2/medialist/resource/list?type=5&oid=%s&otype=2&biz_id=%s&bvid=&with_current=true&mobi_app=web&ps=20&direction=false&sort_field=1&tid=0&desc=true", oid, bizID)
		resp, err := f.client.GetWebSource(ctx, listAPI)
		if err != nil {
			return nil, err
		}
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(resp), &root); err != nil {
			return nil, err
		}
		data := gm(root, "data")
		if data == nil {
			return nil, fmt.Errorf("获取系列分页列表失败(code=%d): %s", gi(root, "code"), gs(root, "message"))
		}
		hasMore = data["has_more"] == true
		previousOid := oid
		for _, m := range ga(data, "media_list") {
			mm, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			oid = gs(mm, "id")
			if gi(mm, "attr") != 0 {
				continue
			}
			pageCount := gi(mm, "page")
			descText := gs(mm, "intro")
			ownerName := gs(gm(mm, "upper"), "name")
			ownerMid := gs(gm(mm, "upper"), "mid")
			for _, pg := range ga(mm, "pages") {
				pm, ok := pg.(map[string]interface{})
				if !ok {
					continue
				}
				title := gs(mm, "title")
				if pageCount != 1 {
					title = fmt.Sprintf("%s_P%s_%s", gs(mm, "title"), gs(pm, "page"), gs(pm, "title"))
				}
				p := entity.Page{
					Index:     index,
					Aid:       gs(mm, "id"),
					Cid:       gs(pm, "id"),
					Title:     title,
					Dur:       gi(pm, "duration"),
					Res:       dimensionRes(pm),
					PubTime:   gi64(mm, "pubtime"),
					Cover:     gs(mm, "cover"),
					Desc:      descText,
					OwnerName: ownerName,
					OwnerMid:  ownerMid,
				}
				index++
				if pageDup(pagesInfo, p.Aid, p.Cid) {
					index--
					continue
				}
				pagesInfo = append(pagesInfo, p)
			}
		}
		if hasMore && oid == previousOid {
			util.LogDebug("系列翻页游标未推进（oid=%s），停止翻页", oid)
			break
		}
	}
	return pagesInfo, nil
}
