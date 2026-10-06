package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QC3284/BBDown-Go/internal/util"
)

// ---- t47：mid: 订阅的增量扫描（默认）与 --full-scan ----
//
// 夹具是一个按端点分流的假服务器：空间列举（分页）、UP 主信息、稿件详情（**只用来数次数**）。
// 仓库纪律：不真连网；增量扫描的核心断言就是「详情端点一次都没被请求」。

type spaceScanServer struct {
	mu       sync.Mutex
	searches int
	views    int
	userInfo int
	// pages[pn] = 该页的 vlist（aid 与标题）
	pages   map[int][]map[string]interface{}
	total   int
	visited []string
}

func (s *spaceScanServer) count(which string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch which {
	case "search":
		return s.searches
	case "view":
		return s.views
	default:
		return s.userInfo
	}
}

func (s *spaceScanServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.visited = append(s.visited, r.URL.Path)
		switch {
		case strings.HasPrefix(r.URL.Path, "/live_user/"):
			s.userInfo++
		case strings.HasPrefix(r.URL.Path, "/x/space/"):
			s.searches++
		case strings.HasPrefix(r.URL.Path, "/x/web-interface/view"):
			s.views++
		}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/live_user/"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"info":{"uname":"某UP"}}}`))
		case strings.HasPrefix(r.URL.Path, "/x/space/"):
			pn, _ := strconv.Atoi(r.URL.Query().Get("pn"))
			vlist, ok := s.pages[pn]
			if !ok {
				vlist = []map[string]interface{}{}
			}
			body, _ := json.Marshal(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"list": map[string]interface{}{"vlist": vlist},
					"page": map[string]interface{}{"count": s.total},
				},
			})
			_, _ = w.Write(body)
		default: // 稿件详情
			body, _ := json.Marshal(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"bvid": "BV1xx411c7mD", "pic": "cover-detail", "title": "详情标题",
					"pages": []map[string]interface{}{{"cid": 1, "page": 1, "part": "P1", "duration": 10}},
				},
			})
			_, _ = w.Write(body)
		}
	}
}

// withFakeSpaceEndpoints 把三个端点都指向假服务器，返回还原函数。
func withFakeSpaceEndpoints(t *testing.T, srv *httptest.Server) func() {
	t.Helper()
	oldSearch, oldUser, oldView := spaceSearchAPI, spaceUserInfoAPI, normalViewAPI
	base := srv.URL
	spaceSearchAPI, spaceUserInfoAPI, normalViewAPI = base+"/x/space/wbi/arc/search", base+"/live_user/v1/Master/info", base+"/x/web-interface/view"
	return func() { spaceSearchAPI, spaceUserInfoAPI, normalViewAPI = oldSearch, oldUser, oldView }
}

func entry(aid int, title string) map[string]interface{} {
	return map[string]interface{}{"aid": aid, "title": title, "pic": "cover-" + title, "description": "desc-" + title, "created": 1700000000}
}

// TestSpaceIncrementalScanSkipsPerEntryDetails 是 t47 ② 的核心断言：
// 默认（增量）模式下 mid: 订阅的检查**不发**逐投稿详情请求——只列举 aid/标题/封面/发布时间。
// 控制组（Incremental=false，旧行为）必须真的请求详情端点，否则「0 次」这条断言可能是假绿。
//
// 变异验证：把 Fetch 里的 f.opts.Incremental 分支去掉（回到 expandEntries）→ 增量臂红（views>0）。
func TestSpaceIncrementalScanSkipsPerEntryDetails(t *testing.T) {
	fake := &spaceScanServer{pages: map[int][]map[string]interface{}{
		1: {entry(170001, "T1"), entry(170002, "T2")},
	}, total: 2}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	defer withFakeSpaceEndpoints(t, srv)()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	f := NewSpaceVideoFetcher(client, "test_wbi", "", SpaceScanOptions{Incremental: true})
	vInfo, err := f.Fetch(context.Background(), "mid:123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got := fake.count("view"); got != 0 {
		t.Errorf("增量扫描不该发逐投稿详情请求，实际 %d 次（请求过的路径：%v）", got, fake.visited)
	}
	if got := fake.count("search"); got != 1 {
		t.Errorf("空间列举应当只翻 1 页，实际 %d 次", got)
	}
	if len(vInfo.PagesInfo) != 2 {
		t.Fatalf("应当按投稿数产出 2 页（一投稿一页），实际 %d：%+v", len(vInfo.PagesInfo), vInfo.PagesInfo)
	}
	want := []struct {
		aid   string
		title string
	}{{"170001", "T1"}, {"170002", "T2"}}
	for i, w := range want {
		p := vInfo.PagesInfo[i]
		if p.Aid != w.aid || p.Title != w.title {
			t.Errorf("第 %d 页 = (aid=%q, title=%q), want (%q, %q)", i, p.Aid, p.Title, w.aid, w.title)
		}
		if p.Index != i+1 {
			t.Errorf("第 %d 页的 Index = %d, want %d", i, p.Index, i+1)
		}
		if p.Cover != "cover-"+w.title || p.Desc != "desc-"+w.title {
			t.Errorf("第 %d 页的封面/简介应当取自轻量列举：cover=%q desc=%q", i, p.Cover, p.Desc)
		}
		if p.PubTime != 1700000000 {
			t.Errorf("第 %d 页的发布时间应当取自轻量列举，实际 %d", i, p.PubTime)
		}
	}

	// 控制组：旧行为（Incremental=false）必须请求详情，证明计数器有效。
	before := fake.count("view")
	if _, err := NewSpaceVideoFetcher(client, "test_wbi", "", SpaceScanOptions{}).Fetch(context.Background(), "mid:123"); err != nil {
		t.Fatalf("控制组 Fetch: %v", err)
	}
	if got := fake.count("view") - before; got != 2 {
		t.Errorf("非增量模式应当为每个投稿请求详情（2 次），实际 %d 次", got)
	}
}

// TestSpaceFullScanIgnoresFullyDownloadedPage 是 t47 ③ 的两臂：
//   - 默认（增量）：第 1 页整页都在订阅历史里 → 停止翻页（省掉旧页的列举成本，代价是历史不完整时会漏旧稿）；
//   - --full-scan：无视这条提前结束，继续翻到最后一页（首次部署/历史重建用）。
//
// 变异验证：去掉 allDownloaded 判定 → 第一臂红（search 次数变 2）；把 FullScan 判定反过来 → 第二臂红。
func TestSpaceFullScanIgnoresFullyDownloadedPage(t *testing.T) {
	// 一页 50 条（与 spacePageSize 一致）：第 1 页整页是旧稿、第 2 页只有一条新稿。
	page1 := make([]map[string]interface{}, 0, spacePageSize)
	for i := 200000; i < 200000+spacePageSize; i++ {
		page1 = append(page1, entry(i, fmt.Sprintf("旧稿%d", i)))
	}
	fake := &spaceScanServer{pages: map[int][]map[string]interface{}{
		1: page1,
		2: {entry(300001, "新稿")},
	}, total: spacePageSize + 1}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	defer withFakeSpaceEndpoints(t, srv)()

	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	downloaded := func(aid string) bool { return strings.HasPrefix(aid, "2000") }

	var logs strings.Builder
	restore := util.RedirectConsoleLogs(&logs)
	incremental, err := NewSpaceVideoFetcher(client, "test_wbi", "", SpaceScanOptions{
		Incremental: true, Downloaded: downloaded,
	}).Fetch(context.Background(), "mid:123")
	restore()
	if err != nil {
		t.Fatalf("增量 Fetch: %v", err)
	}
	if got := fake.count("search"); got != 1 {
		t.Errorf("默认增量：整页已下载就该停止翻页（应该只列举 1 页），实际 %d 页", got)
	}
	if !strings.Contains(logs.String(), "--full-scan") {
		t.Errorf("停止翻页时必须提示 --full-scan，实际日志：%q", logs.String())
	}
	if len(incremental.PagesInfo) != spacePageSize {
		t.Errorf("第一页的 %d 个投稿仍应返回（历史过滤在调用方），实际 %d", spacePageSize, len(incremental.PagesInfo))
	}

	// --full-scan：翻过停止点，拿到第 2 页的新稿。
	beforeSearch := fake.count("search")
	full, err := NewSpaceVideoFetcher(client, "test_wbi", "", SpaceScanOptions{
		Incremental: true, FullScan: true, Downloaded: downloaded,
	}).Fetch(context.Background(), "mid:123")
	if err != nil {
		t.Fatalf("full-scan Fetch: %v", err)
	}
	if got := fake.count("search") - beforeSearch; got != 2 {
		t.Errorf("--full-scan 应当翻完全部 2 页，实际 %d", got)
	}
	if len(full.PagesInfo) != spacePageSize+1 {
		t.Fatalf("--full-scan 应当拿到两页共 %d 个投稿，实际 %d", spacePageSize+1, len(full.PagesInfo))
	}
	if got := full.PagesInfo[0].Aid; got != "200000" {
		t.Errorf("顺序应当保持接口的发布时间倒序：首条 = %q, want 200000", got)
	}
	if got := full.PagesInfo[len(full.PagesInfo)-1].Aid; got != "300001" {
		t.Errorf("最后一页的新稿应当被取到：%q, want 300001", got)
	}
}

// TestIncrementalPagesPreserveEntryFields 钉住纯函数：一投稿一页、Index 递增、字段原样带过来。
func TestIncrementalPagesPreserveEntryFields(t *testing.T) {
	entries := []spaceEntry{
		{Aid: "1", Title: "A", Cover: "ca", Description: "da", PubTime: 11},
		{Aid: "2", Title: "B", Cover: "cb", Description: "db", PubTime: 22},
	}
	pages := incrementalPages(entries, "某UP")
	if len(pages) != 2 {
		t.Fatalf("应当产出 2 页，实际 %d", len(pages))
	}
	if pages[0].Index != 1 || pages[1].Index != 2 {
		t.Errorf("Index 应当递增：%d, %d", pages[0].Index, pages[1].Index)
	}
	if pages[1].Aid != "2" || pages[1].Title != "B" || pages[1].Cover != "cb" || pages[1].Desc != "db" || pages[1].PubTime != 22 {
		t.Errorf("字段没有原样带过来：%+v", pages[1])
	}
	if pages := incrementalPages(nil, "某UP"); len(pages) != 0 {
		t.Errorf("空输入应当返回空切片，实际 %+v", pages)
	}
}

// TestAllDownloadedSemantics 钉住「整页」的判定：空页不算（交给调用方处理空页），
// 有一个没下过就不算整页。
func TestAllDownloadedSemantics(t *testing.T) {
	downloaded := func(aid string) bool { return aid == "1" }
	if allDownloaded(nil, downloaded) {
		t.Error("空页不该算「整页已下载」")
	}
	if !allDownloaded([]spaceEntry{{Aid: "1"}}, downloaded) {
		t.Error("单个已下载的投稿应当算整页")
	}
	if allDownloaded([]spaceEntry{{Aid: "1"}, {Aid: "2"}}, downloaded) {
		t.Error("页里有没下过的投稿时不该算整页")
	}
}
