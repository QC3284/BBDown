package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// FavListFetcher fetches a favorite list (upstream: favId:fid:mid form, default
// folder lookup, pagination, attr filtering, multi-page expansion via details).
type FavListFetcher struct {
	client *util.HTTPClient
}

func (f *FavListFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	rest := strings.TrimPrefix(id, "favId:")
	parts := strings.SplitN(rest, ":", 2)
	favID := parts[0]
	mid := ""
	if len(parts) > 1 {
		mid = parts[1]
	}

	// Default favorite folder: resolve via the created folder list (upstream).
	if favID == "" {
		if mid == "" {
			return nil, fmt.Errorf("收藏夹ID格式错误，期望 favId:收藏夹ID:用户ID")
		}
		favListAPI := fmt.Sprintf("https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid=%s", mid)
		resp, err := f.client.GetWebSource(ctx, favListAPI)
		if err != nil {
			return nil, err
		}
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(resp), &root); err != nil {
			return nil, err
		}
		list := ga(gm(root, "data"), "list")
		if len(list) == 0 {
			return nil, fmt.Errorf("该用户没有创建收藏夹")
		}
		if first, ok := list[0].(map[string]interface{}); ok {
			favID = gs(first, "id")
		}
		if favID == "" {
			return nil, fmt.Errorf("该用户没有创建收藏夹")
		}
	}

	pageSize := 20
	api := fmt.Sprintf("https://api.bilibili.com/x/v3/fav/resource/list?media_id=%s&pn=1&ps=%d&order=mtime&type=2&tid=0&platform=web", favID, pageSize)
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
		return nil, fmt.Errorf("获取收藏夹信息失败(code=%d): %s", gi(root, "code"), gs(root, "message"))
	}
	info := gm(data, "info")
	totalCount := gi(info, "media_count")
	totalPage := (totalCount + pageSize - 1) / pageSize
	title := gs(info, "title")
	intro := gs(info, "intro")
	pubTime := gi64(info, "ctime")

	var pagesInfo []entity.Page
	var failures []string
	index := 1

	processPage := func(pageData map[string]interface{}) {
		for _, m := range ga(pageData, "medias") {
			mm, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			// Skip invalid entries (attr != 0).
			if gi(mm, "attr") != 0 {
				continue
			}
			pageCount := gi(mm, "page")
			if pageCount > 1 {
				// Multi-page entry: expand via the view API (upstream).
				detail, err := (&NormalInfoFetcher{client: f.client}).Fetch(ctx, gs(mm, "id"))
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					failures = append(failures, fmt.Sprintf("av%s %s —— %v", gs(mm, "id"), gs(mm, "title"), err))
					continue
				}
				for _, item := range detail.PagesInfo {
					p := entity.Page{
						Index:     index,
						Aid:       item.Aid,
						Cid:       item.Cid,
						Title:     fmt.Sprintf("%s_P%d_%s", gs(mm, "title"), item.Index, item.Title),
						Dur:       item.Dur,
						Res:       item.Res,
						PubTime:   item.PubTime,
						Cover:     detail.Pic,
						Desc:      gs(mm, "intro"),
						OwnerName: item.OwnerName,
						OwnerMid:  item.OwnerMid,
					}
					index++
					if pageDup(pagesInfo, p.Aid, p.Cid) {
						index--
						continue
					}
					pagesInfo = append(pagesInfo, p)
				}
			} else {
				p := entity.Page{
					Index:     index,
					Aid:       gs(mm, "id"),
					Cid:       gs(gm(mm, "ugc"), "first_cid"),
					Title:     gs(mm, "title"),
					Dur:       gi(mm, "duration"),
					PubTime:   gi64(mm, "pubtime"),
					Cover:     gs(mm, "cover"),
					Desc:      gs(mm, "intro"),
					OwnerName: gs(gm(mm, "upper"), "name"),
					OwnerMid:  gs(gm(mm, "upper"), "mid"),
				}
				index++
				if pageDup(pagesInfo, p.Aid, p.Cid) {
					index--
					continue
				}
				pagesInfo = append(pagesInfo, p)
			}
		}
	}

	processPage(data)
	for page := 2; page <= totalPage; page++ {
		api := fmt.Sprintf("https://api.bilibili.com/x/v3/fav/resource/list?media_id=%s&pn=%d&ps=%d&order=mtime&type=2&tid=0&platform=web", favID, page, pageSize)
		resp, err := f.client.GetWebSource(ctx, api)
		if err != nil {
			break
		}
		var pageRoot map[string]interface{}
		if json.Unmarshal([]byte(resp), &pageRoot) != nil {
			break
		}
		processPage(gm(pageRoot, "data"))
	}

	if len(failures) > 0 {
		util.LogWarn("收藏夹中有 %d 个稿件无法解析，已跳过：", len(failures))
		shown := 10
		if len(failures) < shown {
			shown = len(failures)
		}
		for _, fail := range failures[:shown] {
			util.LogWarn("  %s", fail)
		}
		if len(failures) > 10 {
			util.LogWarn("  ...另有 %d 个", len(failures)-10)
		}
	}

	return &entity.VInfo{
		Title:     strings.TrimSpace(title),
		Desc:      strings.TrimSpace(intro),
		PubTime:   pubTime,
		PagesInfo: pagesInfo,
		IsBangumi: false,
	}, nil
}
