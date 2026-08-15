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

// SpaceVideoFetcher fetches all videos from an uploader's space (upstream
// SpaceVideoFetcher: paginated wbi-signed arc search + per-entry detail
// expansion to obtain cids, with a consecutive-failure limit).
type SpaceVideoFetcher struct {
	client *util.HTTPClient
	wbi    string
	cookie string
}

const (
	spacePageSize                = 50
	spaceConsecutiveFailureLimit = 5
	spaceDetailInterval          = 120 * time.Millisecond
)

type spaceEntry struct {
	Aid, Title, Cover, Description string
	PubTime                        int64
}

type spaceFailure struct {
	Entry  spaceEntry
	Reason string
}

func (f *SpaceVideoFetcher) Fetch(ctx context.Context, id string) (*entity.VInfo, error) {
	mid := strings.TrimPrefix(id, "mid:")

	// The arc search API expects a device identifier; inject buvid3 (upstream).
	if updated := util.EnsureBuvid3(ctx, f.client, f.cookie); updated != f.cookie {
		f.cookie = updated
	}

	// User name via the live API (does not require w_rid).
	userName := ""
	userInfoAPI := "https://api.live.bilibili.com/live_user/v1/Master/info?uid=" + mid
	if resp, err := f.client.GetWebSource(ctx, userInfoAPI); err == nil {
		var r struct {
			Data struct {
				Info struct {
					Uname string `json:"uname"`
				} `json:"info"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(resp), &r) == nil {
			userName = strings.TrimSpace(r.Data.Info.Uname)
		}
	}
	if userName == "" {
		userName = "UP主" + mid
	}

	entries, err := f.fetchAllEntries(ctx, mid, userName)
	if err != nil {
		return nil, err
	}
	util.Log("共 %d 个投稿, 正在获取分P信息...", len(entries))

	pagesInfo, steinGate := f.expandEntries(ctx, entries, userName)
	if len(pagesInfo) == 0 {
		return nil, fmt.Errorf("%s 的投稿均无法解析，请检查登录状态或稍后重试", userName)
	}

	// Single-page space download keeps the video title as the file name.
	title := userName
	if len(pagesInfo) == 1 {
		title = pagesInfo[0].Title
	}

	return &entity.VInfo{
		Title:       strings.TrimSpace(title),
		Desc:        userName + " 的投稿视频",
		PubTime:     entries[0].PubTime,
		PagesInfo:   pagesInfo,
		IsSteinGate: steinGate,
	}, nil
}

func (f *SpaceVideoFetcher) fetchAllEntries(ctx context.Context, mid, userName string) ([]spaceEntry, error) {
	var entries []spaceEntry
	seen := make(map[string]bool)
	pageNumber := 1

	first, totalCount, err := f.fetchPage(ctx, pageNumber, mid)
	if err != nil {
		return nil, err
	}
	addNewEntries(&entries, seen, first)

	if len(entries) == 0 {
		return nil, fmt.Errorf("未获取到 %s 的任何投稿视频", userName)
	}

	totalPage := (totalCount + spacePageSize - 1) / spacePageSize
	for pageNumber < totalPage {
		pageNumber++
		more, _, err := f.fetchPage(ctx, pageNumber, mid)
		if err != nil {
			break
		}
		if len(more) == 0 {
			util.LogWarn("第 %d 页未返回任何投稿，停止翻页（已取到 %d/%d 个）", pageNumber, len(entries), totalCount)
			break
		}
		addNewEntries(&entries, seen, more)
	}

	if totalCount > 0 && len(entries) < totalCount {
		util.LogWarn("接口声称共 %d 个投稿，实际只取到 %d 个", totalCount, len(entries))
	}
	return entries, nil
}

func addNewEntries(target *[]spaceEntry, seen map[string]bool, incoming []spaceEntry) {
	for _, e := range incoming {
		if !seen[e.Aid] {
			seen[e.Aid] = true
			*target = append(*target, e)
		}
	}
}

func (f *SpaceVideoFetcher) fetchPage(ctx context.Context, pageNumber int, mid string) ([]spaceEntry, int, error) {
	params := fmt.Sprintf("mid=%s&order=pubdate&pn=%d&ps=%d&tid=0&wts=%d", mid, pageNumber, spacePageSize, time.Now().Unix())
	api := "https://api.bilibili.com/x/space/wbi/arc/search?" + util.WbiSign(params, f.wbi)
	resp, err := f.client.GetWebSource(ctx, api)
	if err != nil {
		return nil, 0, err
	}

	var r struct {
		Data struct {
			List struct {
				VList []struct {
					Aid         json.Number `json:"aid"`
					Title       string      `json:"title"`
					Pic         string      `json:"pic"`
					Description string      `json:"description"`
					Created     int64       `json:"created"`
				} `json:"vlist"`
			} `json:"list"`
			Page struct {
				Count int `json:"count"`
			} `json:"page"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		return nil, 0, err
	}
	var entries []spaceEntry
	for _, v := range r.Data.List.VList {
		entries = append(entries, spaceEntry{
			Aid:         string(v.Aid),
			Title:       v.Title,
			Cover:       v.Pic,
			Description: v.Description,
			PubTime:     v.Created,
		})
	}
	return entries, r.Data.Page.Count, nil
}

// expandEntries fetches per-video details to obtain cids (upstream).
func (f *SpaceVideoFetcher) expandEntries(ctx context.Context, entries []spaceEntry, userName string) ([]entity.Page, bool) {
	var pagesInfo []entity.Page
	index := 1
	consecutiveFailures := 0
	steinGate := false
	var failures []spaceFailure

	for i, entry := range entries {
		if i > 0 {
			select {
			case <-ctx.Done():
				return pagesInfo, steinGate
			case <-time.After(spaceDetailInterval):
			}
		}

		detail, err := (&NormalInfoFetcher{client: f.client}).Fetch(ctx, entry.Aid)
		if err != nil {
			failures = append(failures, spaceFailure{Entry: entry, Reason: err.Error()})
			consecutiveFailures++
			if consecutiveFailures >= spaceConsecutiveFailureLimit {
				util.LogError("连续 %d 个投稿解析失败，判定为风控或登录状态失效，已中止。最后一次失败：av%s - %s", consecutiveFailures, entry.Aid, err)
				return pagesInfo, steinGate
			}
			continue
		}
		consecutiveFailures = 0
		steinGate = steinGate || detail.IsSteinGate

		multiPage := len(detail.PagesInfo) > 1
		for _, page := range detail.PagesInfo {
			newPage := page
			newPage.Index = index
			index++
			if multiPage {
				newPage.Title = fmt.Sprintf("%s_P%d_%s", entry.Title, page.Index, page.Title)
			} else {
				newPage.Title = entry.Title
			}
			if detail.Pic != "" {
				newPage.Cover = detail.Pic
			} else {
				newPage.Cover = entry.Cover
			}
			newPage.Desc = entry.Description
			pagesInfo = append(pagesInfo, newPage)
		}
	}

	if len(failures) > 0 {
		util.LogWarn("%s 的 %d 个投稿中有 %d 个无法解析，已跳过：", userName, len(entries), len(failures))
		for j, f := range failures {
			if j < 10 {
				util.LogWarn("  av%s %s —— %s", f.Entry.Aid, f.Entry.Title, f.Reason)
			} else {
				util.LogDebug("跳过投稿 av%s（%s）: %s", f.Entry.Aid, f.Entry.Title, f.Reason)
			}
		}
	}
	return pagesInfo, steinGate
}
