package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/QC3284/BBDown-go/internal/entity"
	"github.com/QC3284/BBDown-go/internal/util"
)

// SpaceVideoFetcher fetches all videos from an uploader's space (upstream
// SpaceVideoFetcher: paginated wbi-signed arc search + per-entry detail
// expansion to obtain cids, with a consecutive-failure limit).
type SpaceVideoFetcher struct {
	client *util.HTTPClient
	wbi    string
	cookie string
	// opts 是增量扫描 / 全量扫描选项（零值 = 旧的逐投稿展开行为，供既有调用方沿用）。
	opts SpaceScanOptions
}

// NewSpaceVideoFetcher 造一个带扫描选项的空间投稿 fetcher（t47：sub check 的增量扫描/full-scan）。
func NewSpaceVideoFetcher(client *util.HTTPClient, wbi, cookie string, opts SpaceScanOptions) *SpaceVideoFetcher {
	return &SpaceVideoFetcher{client: client, wbi: wbi, cookie: cookie, opts: opts}
}

const (
	spacePageSize                = 50
	spaceConsecutiveFailureLimit = 5
	spaceDetailInterval          = 120 * time.Millisecond
)

// 测试接缝：空间投稿列举与 UP 主信息两个端点。变量而非常量，离线用例注入假服务器
// （与 internal/util 里那些「给测试留的接缝」同款做法）；生产路径就是这两个常量。
var (
	spaceSearchAPI   = "https://api.bilibili.com/x/space/wbi/arc/search"
	spaceUserInfoAPI = "https://api.live.bilibili.com/live_user/v1/Master/info"
)

type spaceEntry struct {
	Aid, Title, Cover, Description string
	PubTime                        int64
}

// SpaceScanOptions 是 mid: 订阅的扫描选项（t47，吸收上游 1.6.21/1.6.22）。
//
// 默认（Incremental=true）是**增量扫描**：空间列举只取 aid/标题/封面/发布时间，
// 不再逐个投稿发详情请求展开分P——每个新 aid 的下载本来就会各自解析（上游实测 11.75s → 1.00s）。
// FullScan=true 关掉「整页都已下载就停止翻页」的提前结束：首次部署、历史重建、怀疑漏下时用
// （代价是每次都翻到最后，老订阅的大空间会退回逐页列举的成本）。
type SpaceScanOptions struct {
	Incremental bool
	FullScan    bool
	// Downloaded 报告某个 aid 是否已在订阅历史里（nil = 一律当作没下过）。
	Downloaded func(aid string) bool
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
	userInfoAPI := spaceUserInfoAPI + "?uid=" + mid
	if resp, err := f.client.GetWebSource(ctx, userInfoAPI); err == nil {
		var r struct {
			Data struct {
				Info struct {
					Uname string `json:"uname"`
				} `json:"info"`
			} `json:"data"`
		}
		if util.UnmarshalJSON(resp, &r) == nil {
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

	var pagesInfo []entity.Page
	steinGate := false
	if f.opts.Incremental {
		// 增量扫描：不发逐投稿详情请求——每个 aid 的下载阶段会各自解析分P。
		util.Log("共 %d 个投稿（增量扫描：只列举 aid，分P 在下载时各自解析）", len(entries))
		pagesInfo = incrementalPages(entries, userName)
		if len(pagesInfo) == 0 {
			return nil, fmt.Errorf("%s 的投稿均无法解析，请检查登录状态或稍后重试", userName)
		}
	} else {
		util.Log("共 %d 个投稿, 正在获取分P信息...", len(entries))
		pagesInfo, steinGate = f.expandEntries(ctx, entries, userName)
		if len(pagesInfo) == 0 {
			return nil, fmt.Errorf("%s 的投稿均无法解析，请检查登录状态或稍后重试", userName)
		}
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

// incrementalPages 把「轻量列举出来的投稿」直接变成分P 列表：一个投稿一页，
// 不请求详情（t47 ②）。分P 的展开留给下载阶段——那里本来就是按 aid 完整解析一次。
func incrementalPages(entries []spaceEntry, userName string) []entity.Page {
	pagesInfo := make([]entity.Page, 0, len(entries))
	for i, e := range entries {
		pagesInfo = append(pagesInfo, entity.Page{
			Index:   i + 1,
			Aid:     e.Aid,
			Title:   e.Title,
			Cover:   e.Cover,
			Desc:    e.Description,
			PubTime: e.PubTime,
		})
	}
	_ = userName
	return pagesInfo
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

	// 增量模式（默认）：整页都已下载过就没有再往旧页翻的必要——空间列表是按发布时间倒序的，
	// 一页全在历史里说明后面的页更旧。代价是「历史不完整时（首次部署/历史被清/怀疑漏下）会
	// 跳过旧页」，用 --full-scan 关掉这条提前结束。
	if !f.opts.FullScan && f.opts.Downloaded != nil && allDownloaded(first, f.opts.Downloaded) {
		util.Log("第 1 页的 %d 个投稿都已在订阅历史里，停止翻页（如需往前扫请加 --full-scan）", len(first))
		return entries, nil
	}

	totalPage := (totalCount + spacePageSize - 1) / spacePageSize
	for pageNumber < totalPage {
		pageNumber++
		more, _, err := f.fetchPage(ctx, pageNumber, mid)
		if err != nil {
			// Page fetch failure is a systemic error (rate limit / network),
			// not an empty page; propagate it instead of silently truncating.
			return nil, fmt.Errorf("获取第 %d 页投稿失败: %w", pageNumber, err)
		}
		if len(more) == 0 {
			util.LogWarn("第 %d 页未返回任何投稿，停止翻页（已取到 %d/%d 个）", pageNumber, len(entries), totalCount)
			break
		}
		addNewEntries(&entries, seen, more)
		if !f.opts.FullScan && f.opts.Downloaded != nil && allDownloaded(more, f.opts.Downloaded) {
			util.Log("第 %d 页的 %d 个投稿都已在订阅历史里，停止翻页（如需往前扫请加 --full-scan）", pageNumber, len(more))
			break
		}
	}

	if totalCount > 0 && len(entries) < totalCount {
		util.LogWarn("接口声称共 %d 个投稿，实际只取到 %d 个", totalCount, len(entries))
	}
	return entries, nil
}

// allDownloaded 报告这一页的投稿是否**全部**已在订阅历史里（空页不算——空页由调用方处理）。
func allDownloaded(page []spaceEntry, downloaded func(aid string) bool) bool {
	if len(page) == 0 {
		return false
	}
	for _, e := range page {
		if !downloaded(e.Aid) {
			return false
		}
	}
	return true
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
	api := spaceSearchAPI + "?" + util.WbiSign(params, f.wbi)
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
	if err := util.UnmarshalJSON(resp, &r); err != nil {
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
