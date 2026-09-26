package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/drm"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/fetcher"
	"github.com/QC3284/BBDown/internal/login"
	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
	"sync"
)

const backupHost = "upos-sz-mirrorcoso1.bilivideo.com"

var (
	pcdnRegex = regexp.MustCompile(`://[^/]+:\d+/`)
	akamRegex = regexp.MustCompile(`://[^/]*akamaized\.net/`)
	uposRegex = regexp.MustCompile(`://[^/]+/`)
)

// Workflow orchestrates the BBDown download process.
type Workflow struct {
	Cfg        config.MyOption
	HTTPClient *util.HTTPClient

	// sessionWbi is the WBI mixin key extracted from the nav API during
	// initRequestSession; used to sign playurl/space requests.
	sessionWbi string

	// MetaHandler is invoked with the fetched video metadata (serve mode uses
	// it to fill task Title/Pic/VideoPubTime).
	MetaHandler func(*entity.VInfo)

	// OnSaved is invoked with each final output path (serve mode uses it to
	// collect SavePaths).
	OnSaved func(path string)
}

// New creates a new Workflow.
func New(cfg config.MyOption, client *util.HTTPClient) *Workflow {
	return &Workflow{Cfg: cfg, HTTPClient: client}
}

// InitSession prepares a session for non-download commands (watchlater / sub
// check): loads credentials, checks the login state, extracts the WBI key and
// returns it (upstream InitializeRequestSessionAsync).
func InitSession(ctx context.Context, cfg *config.MyOption, client *util.HTTPClient) (string, error) {
	w := &Workflow{Cfg: *cfg, HTTPClient: client}
	w.handleConflictingOptions()
	if err := w.validateNumericOptions(); err != nil {
		return "", err
	}
	w.handleDeprecatedOptions()
	if err := w.applyConfig(); err != nil {
		return "", err
	}
	if err := w.loadCredentials(); err != nil {
		return "", err
	}
	w.initRequestSession(ctx)
	*cfg = w.Cfg
	return w.sessionWbi, nil
}

// Run executes the complete download workflow.
func (w *Workflow) Run(ctx context.Context) error {
	input := w.Cfg.URL
	if input == "" {
		return fmt.Errorf("请提供视频地址")
	}

	// Resolve aid from URL
	aidOri, err := ResolveURL(ctx, w.HTTPClient, input)
	if err != nil {
		return fmt.Errorf("解析链接失败: %w", err)
	}
	util.Log("获取aid结束: %s", aidOri)

	// Fix conflicting options
	w.handleConflictingOptions()
	if err := w.validateNumericOptions(); err != nil {
		return err
	}
	w.handleDeprecatedOptions()

	// Apply config to HTTP client and working dir
	if err := w.applyConfig(); err != nil {
		return err
	}

	// Load credentials from data files
	if err := w.loadCredentials(); err != nil {
		return err
	}

	// Initialize request session: apply credentials to the HTTP client, check
	// login state and extract the WBI mixin key (upstream InitializeRequestSessionAsync).
	w.initRequestSession(ctx)

	// Parse priorities
	encodingPriority, firstEncoding := parseEncodingPriority(w.Cfg.EncodingPriority)
	dfnPriority := parseDfnPriority(w.Cfg.DfnPriority)
	downloadDanmaku := w.Cfg.DownloadDanmaku || w.Cfg.DanmakuOnly
	danmakuFormats, _ := parseDanmakuFormats(w.Cfg.DownloadDanmakuFormats)
	lang := w.Cfg.Language
	delay := w.Cfg.DelayPerPage

	// Fetch video info
	factory := fetcher.NewFactory(w.HTTPClient, w.Cfg.UseIntlAPI, w.sessionWbi, w.Cfg.Cookie, w.Cfg.Host, w.Cfg.EpHost, w.Cfg.AccessToken)
	f := factory.Create(aidOri)
	vInfo, err := f.Fetch(ctx, aidOri)
	if err != nil {
		// Fallback EP→cheese
		if strings.HasPrefix(aidOri, "ep:") {
			util.LogWarn("未找到此 EP/SS 对应番剧信息, 正在尝试按课程查找...")
			cheeseID := strings.Replace(aidOri, "ep:", "cheese:", 1)
			cf := factory.Create(cheeseID)
			vInfo, err = cf.Fetch(ctx, cheeseID)
			aidOri = cheeseID
		}
		if err != nil {
			return fmt.Errorf("获取视频信息失败: %w", err)
		}
	}

	// Report metadata to the caller (serve task fields).
	if w.MetaHandler != nil {
		w.MetaHandler(vInfo)
	}

	printVideoHeader(vInfo, w.Cfg.UseIntlAPI)
	applySteinGateFallback(&w.Cfg, vInfo)

	// API type
	apiType := "WEB"
	if w.Cfg.UseTvAPI {
		apiType = "TV"
	} else if w.Cfg.UseAppAPI {
		apiType = "APP"
	} else if w.Cfg.UseIntlAPI {
		apiType = "INTL"
	}

	// Page summary & selection
	pagesInfo := vInfo.PagesInfo
	selectedPages, err := getSelectedPages(&w.Cfg, vInfo, input)
	if err != nil {
		return err
	}
	selLabel := "ALL"
	if selectedPages != nil {
		parts := make([]string, len(selectedPages))
		for i, s := range selectedPages {
			parts[i] = s
		}
		selLabel = strings.Join(parts, ",")
	}
	util.Log("共计 %d 个分P, 已选择：%s", len(pagesInfo), selLabel)

	// Filter pages if selection specified
	if selectedPages != nil {
		var filtered []entity.Page
		for _, p := range pagesInfo {
			idx := fmt.Sprintf("%d", p.Index)
			for _, s := range selectedPages {
				if s == idx {
					filtered = append(filtered, p)
					break
				}
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("所选分P不存在: %s，视频共有 %d 个分P", selLabel, len(pagesInfo))
		}
		pagesInfo = filtered
	}

	showPages := vInfo.PagesInfo
	if !w.Cfg.ShowAll && len(showPages) > 6 {
		for _, p := range showPages[:5] {
			util.Log("  P%d: [%s] [%s] [%s]", p.Index, p.Cid, p.Title, util.FormatTime(p.Dur, true))
		}
		util.Log("  ......")
		last := showPages[len(showPages)-1]
		util.Log("  P%d: [%s] [%s] [%s]", last.Index, last.Cid, last.Title, util.FormatTime(last.Dur, true))
	} else {
		for _, p := range showPages {
			util.Log("  P%d: [%s] [%s] [%s]", p.Index, p.Cid, p.Title, util.FormatTime(p.Dur, true))
		}
	}

	// Save path format
	pagesCount := len(pagesInfo)
	bangumi := vInfo.IsBangumi
	savePathFormat := resolveSavePathFormat(w.Cfg.FilePattern, w.Cfg.MultiFilePattern, pagesCount, bangumi && !vInfo.IsBangumiEnd)

	// Download config
	dlCfg := download.DownloadConfig{
		UseAria2c:     w.Cfg.UseAria2c,
		Aria2cArgs:    w.Cfg.Aria2cArgs,
		Aria2cPath:    w.Cfg.Aria2cPath,
		ForceHTTP:     w.Cfg.ForceHTTP,
		MultiThread:   w.Cfg.MultiThread,
		SegmentSizeMB: w.Cfg.ThreadSegmentSize,
		RetryCount:    w.Cfg.RetryCount,
		RetryDelayMs:  w.Cfg.RetryDelay,
		Cookie:        w.Cfg.Cookie,
		Client:        w.HTTPClient,
	}

	parserCfg := config.AppSettings{
		Cookie:       w.Cfg.Cookie,
		Token:        w.Cfg.AccessToken,
		Host:         w.Cfg.Host,
		EpHost:       w.Cfg.EpHost,
		TvHost:       w.Cfg.TvHost,
		Area:         w.Cfg.Area,
		Wbi:          w.sessionWbi,
		SkipSslCheck: w.Cfg.Insecure,
		DebugLog:     w.Cfg.Debug,
	}
	parserInst := parser.NewParser(w.HTTPClient, parserCfg)

	// Only pages that still need downloading take part in archiving.
	var archiveAids []string
	for _, p := range pagesInfo {
		if !w.checkAidArchived(p.Aid) {
			archiveAids = append(archiveAids, p.Aid)
		}
	}
	tracker := NewArchiveTracker(archiveAids)

	var failedPages []int
	for _, page := range pagesInfo {
		if pagesCount > 1 && delay > 0 {
			util.Log("停顿%d秒...", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(delay) * time.Second):
			}
		}

		idx := 1
		for i, p := range pagesInfo {
			if p.Index == page.Index {
				idx = i + 1
				break
			}
		}
		util.Log("开始解析P%d: %s... (%d of %d)", page.Index, page.Aid, idx, pagesCount)

		// Archive skip
		if w.checkAidArchived(page.Aid) {
			util.Log("aid: %s 已下载过, 跳过下载...", page.Aid)
			continue
		}

		success := w.downloadOnePage(ctx, parserInst, page, vInfo, pagesInfo, aidOri, savePathFormat, apiType,
			encodingPriority, dfnPriority, firstEncoding, lang, dlCfg,
			downloadDanmaku, danmakuFormats)
		if !success {
			// Ctrl+C 等取消：立即退出，不再继续下一个分P。
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failedPages = append(failedPages, page.Index)
		}
		if w.Cfg.SaveArchivesToFile {
			// The tracker holds the write back until every page of this aid is done.
			if tracker.OnProcessed(page.Aid, success) {
				w.saveAidArchived(page.Aid)
				util.LogDebug("aid: %s 全部分P已完成, 已记录到 BBDown.archives", page.Aid)
			}
		}
	}

	if w.Cfg.NotifyWebhook != "" {
		w.notifyCompletion(vInfo, len(failedPages) == 0)
	}

	if len(failedPages) > 0 {
		parts := make([]string, len(failedPages))
		for i, n := range failedPages {
			parts[i] = fmt.Sprintf("P%d", n)
		}
		return fmt.Errorf("共 %d 个分P下载失败：%s", len(failedPages), strings.Join(parts, ", "))
	}

	util.Log("任务完成")
	return nil
}

func (w *Workflow) downloadOnePage(ctx context.Context, p *parser.Parser, page entity.Page, vInfo *entity.VInfo,
	allPages []entity.Page, aidOri, savePathFormat, apiType string,
	encodingPriority map[string]int, dfnPriority map[string]int, firstEncoding, lang string,
	dlCfg download.DownloadConfig, downloadDanmaku bool, danmakuFormats []string) bool {

	title := vInfo.Title
	pic := vInfo.Pic
	pubTime := vInfo.PubTime
	pagesCount := len(allPages)

	// Sanitize title
	if strings.HasSuffix(title, ".") {
		title += "_fix"
	}
	if strings.HasPrefix(title, ".") {
		title = "_" + title
	}

	// The product lock is taken once for the whole page and released when this
	// function returns. Acquiring it per attempt deadlocked the retry path: the
	// lock is held until the page finishes, sync.Mutex is not reentrant, and a
	// mutex wait ignores context cancellation — so Ctrl+C could not interrupt it.
	var productLock pageLock
	defer productLock.unlock()

	// Page-level retry is fixed at 3 (upstream); per-request retries live in the
	// downloader and honor --retry-count/--retry-delay.
	const pageRetryLimit = 3
	retryDelay := time.Duration(w.Cfg.RetryDelay) * time.Millisecond
	if retryDelay <= 0 {
		retryDelay = 3 * time.Second
	}

	// 交互选择只询问一次（上游用 selected 标志避免下载失败重试时反复询问）；
	// 与上游不同的是：重试时复用用户的选择，而不是退回默认 0。
	vIndex := 0
	aIndex := 0
	selectionAsked := false
	// FLV 分支：用户选择的清晰度跨重试复用，重试时用同一清晰度重新解析
	// （上游重试时不重新解析，会拿到错误清晰度的片段）。
	flvDfnPicked := false
	var selectedDfn string

	for retry := 0; retry < pageRetryLimit; retry++ {
		// Fetch chapter/view points (upstream FetchPointsAsync; failure degrades
		// to a warning and an empty chapter list).
		//
		// -I 只打印流信息，章节不会进产物（混流在 OnlyShowInfo 之前就返回了）：这条
		// player/wbi/v2 请求纯属浪费，单次解析的 5 个 GET 里它占一个（优化，见 ROADMAP O2）。
		if !w.Cfg.OnlyShowInfo {
			page.Points = fetchPointsFunc(ctx, w.HTTPClient, page.Cid, page.Aid)
		}

		// Parse tracks
		result, err := p.ExtractTracks(ctx, aidOri, page.Aid, page.Cid, page.Epid,
			w.Cfg.UseTvAPI, w.Cfg.UseIntlAPI, w.Cfg.UseAppAPI, firstEncoding, w.Cfg.DecryptDrm, "")
		if err != nil {
			attempt := retry + 1
			if attempt >= pageRetryLimit {
				util.LogError("P%d 解析失败（重试%d次后）: %v", page.Index, attempt, err)
				return false
			}
			logPageRetry(err, retryDelay)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(retryDelay):
			}
			continue
		}

		// 充电专属视频试看检测（上游：在排序/打印/下载前拦截，避免把试看片段当成功产物）
		verdict := util.InspectUpower(vInfo.IsUpowerExclusive, vInfo.IsUpowerPlay, page.Dur, result.ActualDurationSec)
		if verdict.IsPreview {
			util.LogWarn("========================================")
			util.LogWarn("  充电专属视频")
			util.LogWarn("  %s", verdict.Reason)
			if !w.Cfg.AllowPreview && !w.Cfg.OnlyShowInfo {
				util.LogWarn("  已跳过。如需下载试看片段，请加 --allow-preview")
				util.LogWarn("========================================")
				return false
			}
			if w.Cfg.OnlyShowInfo {
				util.LogWarn("  仅解析模式，以下流信息对应的是试看片段")
				util.LogWarn("========================================")
			} else {
				util.LogWarn("  已启用 --allow-preview，将下载试看片段")
				util.LogWarn("========================================")
				// 标记在标题上而非拼接路径：<videoTitle> 是所有产物共用的
				// 占位符，改这里能一次覆盖视频/封面/弹幕，也不破坏自定义模板。
				// 幂等保护：下载失败重试会重新经过这里，避免前缀重复叠加。
				if !strings.HasPrefix(title, "[试看]") {
					title = "[试看]" + title
				}
			}
		}

		// Debug: dump the raw playurl JSON next to the output, keep the last 20.
		if w.Cfg.Debug {
			debugFile := fmt.Sprintf("debug_%s.json", time.Now().Format("20060102150405.000"))
			if err := os.WriteFile(debugFile, []byte(result.WebJSONString), 0o644); err == nil {
				files, _ := filepath.Glob("debug_*.json")
				sort.Strings(files)
				for i := 0; i+20 < len(files); i++ {
					os.Remove(files[i])
				}
			}
		}

		// Sort
		result.VideoTracks = download.SortVideoTracks(result.VideoTracks, dfnPriority, encodingPriority, w.Cfg.VideoAscending)
		result.AudioTracks = download.SortAudioTracks(result.AudioTracks, encodingPriority, w.Cfg.AudioAscending)

		// 零轨道：dash 分支里没有任何视频流/音频流时要说出来，并按 only 模式判定失败
		// （上游 Download.cs:553-563）。此前只清空轨道不报错，--video-only 遇到无视频流的
		// 稿件会一件产物都不出却报成功（假成功）。
		if (len(result.VideoTracks) > 0 || len(result.AudioTracks) > 0) && len(result.Clips) == 0 {
			if len(result.VideoTracks) == 0 {
				util.LogWarn("没有找到符合要求的视频流")
				if w.Cfg.VideoOnly {
					return false
				}
			}
			if len(result.AudioTracks) == 0 {
				util.LogWarn("没有找到符合要求的音频流")
				if w.Cfg.AudioOnly {
					return false
				}
			}
		}

		// Clear tracks for --audio-only / --video-only BEFORE display
		if w.Cfg.AudioOnly {
			result.VideoTracks = nil
		}
		if w.Cfg.VideoOnly {
			result.AudioTracks = nil
			result.BackgroundAudioTracks = nil
			result.RoleAudioList = nil
		}

		if !w.Cfg.HideStreams {
			download.PrintAllTracks(result, page.Dur, w.Cfg.OnlyShowInfo)
		}

		if w.Cfg.OnlyShowInfo {
			return true
		}

		// Select tracks (interactive or default)
		var selectedVideo *entity.Video
		var selectedAudio *entity.Audio

		if w.Cfg.Interactive && !selectionAsked {
			if len(result.VideoTracks) > 0 {
				fmt.Print("请选择一条视频流(输入序号): ")
				fmt.Print(util.AnsiCyan)
				var ok bool
				vIndex, ok = readIntSafe(ctx)
				fmt.Print("\033[0m")
				if !ok {
					// Ctrl+C 中断：直接取消，不把取消当作"选择 0"继续下载。
					return false
				}
				if vIndex < 0 || vIndex >= len(result.VideoTracks) {
					vIndex = 0
				}
			}
			if len(result.AudioTracks) > 0 {
				fmt.Print("请选择一条音频流(输入序号): ")
				fmt.Print(util.AnsiCyan)
				var ok bool
				aIndex, ok = readIntSafe(ctx)
				fmt.Print("\033[0m")
				if !ok {
					return false
				}
				if aIndex < 0 || aIndex >= len(result.AudioTracks) {
					aIndex = 0
				}
			}
			selectionAsked = true
		}

		if len(result.VideoTracks) > vIndex {
			selectedVideo = &result.VideoTracks[vIndex]
		}
		if len(result.AudioTracks) > aIndex {
			selectedAudio = &result.AudioTracks[aIndex]
		}

		// PCDN / host replace
		if w.Cfg.ForceReplaceHost && w.Cfg.UposHost == "" {
			w.Cfg.UposHost = backupHost
		}
		// 先记下替换前的原地址：替换后的目标返回 404 时下载器用它回退一次
		// （本仓有意差异，见 docs/UPSTREAM_ALIGNMENT.md §4.33）。
		var origVideoURL, origAudioURL string
		if selectedVideo != nil {
			origVideoURL = selectedVideo.BaseURL
		}
		if selectedAudio != nil {
			origAudioURL = selectedAudio.BaseURL
		}
		handlePcdn(&w.Cfg, selectedVideo, selectedAudio)

		util.Log("已选择的流:")
		download.PrintSelectedTrack(selectedVideo, selectedAudio, page.Dur)

		// HDR Vivid(129) 兼容提醒：它是「最高档」而非「最稳档」——不支持的屏幕/播放器上会偏色、发灰
		// 甚至无法播放，而用户往往只看到「选了最高清晰度」。只提醒、不阻断：换档用 --dfn/-Q。
		if selectedVideo != nil && selectedVideo.ID == config.HDRVividID {
			util.LogWarn("所选清晰度为 HDR Vivid(%s)：需要支持 HDR Vivid 的设备与播放器，不兼容时画面会偏色或无法播放；想要通用兼容性可用 --dfn/-Q 选择普通档位", config.HDRVividID)
		}

		// --print-urls：只把所选流的直链打出来（一行一个），不下载——方便接 aria2c/脚本（本仓特色）。
		if w.Cfg.PrintURLs {
			if selectedVideo != nil && selectedVideo.BaseURL != "" {
				fmt.Println(selectedVideo.BaseURL)
			}
			for _, clip := range result.Clips {
				fmt.Println(clip)
			}
			if selectedAudio != nil && selectedAudio.BaseURL != "" {
				fmt.Println(selectedAudio.BaseURL)
			}
			return true
		}

		// Save path
		savePath := download.FormatSavePath(savePathFormat, title, selectedVideo, selectedAudio, page, pagesCount, apiType, pubTime)
		// Ensure mp4 extension for muxer output
		if !strings.HasSuffix(strings.ToLower(savePath), ".mp4") && !w.Cfg.AudioOnly {
			savePath += ".mp4"
		}
		if w.Cfg.AudioOnly && !strings.HasSuffix(strings.ToLower(savePath), ".m4a") {
			savePath += ".m4a"
		}
		util.LogDebug("SavePath: %s", savePath)

		// Hold the product lock across the existence check, the download and the mux
		// (acquired once for the page; see the declaration above).
		productLock.acquire(savePath)

		// Skip if exists（上游语义）。--overwrite 时跳过这一步：归档里想重下/重编码时需要它，
		// 否则只能手动删文件（本仓新增开关，见 §4.44）。
		if shouldSkipProduct(w.Cfg.Overwrite, savePath) {
			util.Log("%s 已存在, 跳过下载...", savePath)
			return true
		}
		if w.Cfg.Overwrite {
			if _, err := os.Stat(savePath); err == nil {
				util.Log("--overwrite：忽略已存在的 %s，重新下载", savePath)
			}
		}

		// Cover (normal path — skipped when in "only" mode)
		if !w.Cfg.SkipCover && !w.Cfg.SubOnly && !w.Cfg.DanmakuOnly && !w.Cfg.CoverOnly {
			coverURL := pic
			if coverURL == "" {
				coverURL = page.Cover
			}
			if coverURL != "" {
				coverPath := filepath.Join(page.Aid, page.Aid+".jpg")
				os.MkdirAll(page.Aid, 0755)
				if err := download.DownloadFile(ctx, coverURL, coverPath, dlCfg); err != nil {
					util.LogWarn("封面下载失败（已跳过）: %v", err)
				}
			}
		}

		// Cover-only: save the cover under the output name and stop right there.
		// The previous code carried a "matching C#: no early return" comment, but
		// upstream does return: "封面保存成功后必须立即 return，否则会继续执行下方
		// 轨道解析、视频/音频下载与混流——用户只要封面却白白下载完整视频".
		if w.Cfg.CoverOnly {
			coverURL := pic
			if coverURL == "" {
				coverURL = page.Cover
			}
			if coverURL == "" {
				// No cover resource: fail the page instead of reporting a zero-output
				// success whose SavePath points at a file that was never created.
				util.LogWarn("CoverOnly 模式无封面资源可下载")
				return false
			}
			coverExt := filepath.Ext(coverURL)
			if idx := strings.Index(coverExt, "?"); idx >= 0 {
				coverExt = coverExt[:idx]
			}
			newCover := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + coverExt
			os.MkdirAll(filepath.Dir(newCover), 0755)
			if err := download.DownloadFile(ctx, coverURL, newCover, dlCfg); err != nil {
				util.LogWarn("封面下载失败: %v", err)
				return false
			}
			// Only drop the work dir when nothing else is in it.
			if entries, err := os.ReadDir(page.Aid); err == nil && len(entries) == 0 {
				os.Remove(page.Aid)
			}
			if w.OnSaved != nil {
				w.OnSaved(newCover)
			}
			return true
		}

		// Danmaku
		if downloadDanmaku && !w.Cfg.OnlyShowInfo {
			danmakuURL := fmt.Sprintf("https://comment.bilibili.com/%s.xml", page.Cid)
			xmlPath := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + ".xml"
			os.MkdirAll(filepath.Dir(xmlPath), 0755)
			util.Log("正在下载弹幕Xml文件")
			if err := download.DownloadFile(ctx, danmakuURL, xmlPath, dlCfg); err != nil {
				util.LogWarn("弹幕下载失败: %v", err)
			} else {
				items, err := util.ParseDanmakuXML(xmlPath)
				if err != nil || len(items) == 0 {
					util.Log("当前视频没有弹幕, 删除Xml...")
					os.Remove(xmlPath)
				} else {
					assPath := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + ".ass"
					filtered := util.FilterDanmaku(items, w.Cfg.DanmakuFilter, w.Cfg.DanmakuFilterUser)
					if len(filtered) > 0 {
						util.Log("正在保存弹幕Ass文件...")
						util.SaveDanmakuAsASS(filtered, assPath)
					}
				}
			}
			if w.Cfg.DanmakuOnly {
				os.RemoveAll(page.Aid)
				return true
			}
		}

		// Subtitle
		var downloadedSubs []entity.Subtitle
		var backgroundMaterial []entity.AudioMaterial
		if !w.Cfg.SkipSubtitle && !w.Cfg.DanmakuOnly && !w.Cfg.CoverOnly && !w.Cfg.OnlyShowInfo {
			util.LogDebug("获取字幕...")
			subs, _ := util.GetSubtitles(ctx, w.HTTPClient, page.Aid, page.Cid, page.Epid, page.Index, w.Cfg.UseIntlAPI, w.Cfg.Cookie)
			// 显式要字幕却一个字幕都没有时，必须说清楚——否则用户只看到「任务完成」却没有任何产物，
			// 分不清「该视频没有字幕」「没登录」和「下载失败」（真机实测踩到，见 §4.43）。
			if len(subs) == 0 && w.Cfg.SubOnly {
				util.LogWarn("未找到可用字幕：可能需要登录（bbdown login）、选择别的语言，或该视频没有字幕")
			}
			if w.Cfg.SkipAi && len(subs) > 0 {
				var filtered []entity.Subtitle
				for _, s := range subs {
					if !strings.HasPrefix(s.Lan, "ai-") {
						filtered = append(filtered, s)
					}
				}
				subs = filtered
			}
			for _, s := range subs {
				util.Log("下载字幕 %s => %s...", s.Lan, strings.ReplaceAll(util.SubCode2(s.Lan), "_", ""))
				util.LogDebug("下载：%s", s.URL)
				if err := util.SaveSubtitle(w.HTTPClient, s.URL, s.Path); err != nil {
					util.LogWarn("字幕下载失败: %v", err)
					continue
				}
				if _, err := os.Stat(s.Path); err == nil {
					downloadedSubs = append(downloadedSubs, s)
				}
			}
			if w.Cfg.SubOnly {
				for _, s := range subs {
					if _, err := os.Stat(s.Path); err == nil {
						outPath := download.FormatSavePath(savePathFormat, title, nil, nil, page, pagesCount, apiType, pubTime)
						// Keep the extension of the file that was actually downloaded: forcing
						// ".srt" onto an ASS or JSON track produced a file whose contents
						// contradict its name (upstream RF-18).
						ext := filepath.Ext(s.Path)
						if ext == "" {
							ext = ".srt"
						}
						outPath = strings.TrimSuffix(outPath, filepath.Ext(outPath)) + "." + util.SanitizePathSegment(s.Lan) + ext
						os.MkdirAll(filepath.Dir(outPath), 0755)
						os.Rename(s.Path, outPath)
					}
				}
				os.RemoveAll(page.Aid)
				return true
			}
		}

		// Comments (upstream: first page only, aid must be numeric; failures are
		// downgraded to warnings, cancellation aborts).
		if w.Cfg.DownloadComments && page.Index == 1 {
			if _, err := strconv.ParseInt(page.Aid, 10, 64); err == nil {
				util.Log("获取评论区...")
				comments, truncated, err := util.FetchComments(ctx, w.HTTPClient, page.Aid, 20)
				if err != nil {
					if ctx.Err() != nil {
						return false
					}
					util.LogWarn("评论下载失败（已跳过）: %v", err)
				} else {
					commentsPath := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + ".comments.json"
					if err := util.SaveCommentsJSON(comments, commentsPath); err != nil {
						util.LogWarn("评论保存失败（已跳过）: %v", err)
					}
					if truncated {
						util.LogWarn("评论数量达到抓取上限（%d 条），可能还有更多评论未导出", len(comments))
					}
				}
			}
		}

		// Media output paths (dash downloads or FLV merge fill these).
		videoPath := ""
		audioPath := ""

		// FLV 分段流 (upstream flv branch): 视频轨道没有 base_url 时使用 durl 分段。
		if selectedVideo != nil && selectedVideo.BaseURL == "" && len(result.Clips) > 0 && len(result.Dfns) > 0 {
			if w.Cfg.DecryptDrm {
				util.LogError("此视频需要大会员登录才能获取完整DRM内容。")
				util.LogError("请先运行: BBDown login  或使用 --cookie 参数")
				return false
			}
			if w.Cfg.Interactive && !flvDfnPicked {
				for i, q := range result.Dfns {
					util.LogColorNoTime("%d.%s", i, config.QualityMap[q])
				}
				fmt.Print("请选择最想要的清晰度(输入序号): ")
				fmt.Print(util.AnsiCyan)
				qi, ok := readIntSafe(ctx)
				fmt.Print(util.AnsiReset)
				if !ok {
					return false
				}
				if qi >= len(result.Dfns) || qi < 0 {
					qi = 0
				}
				selectedDfn = result.Dfns[qi]
				flvDfnPicked = true
			}
			if flvDfnPicked {
				reResult, err := p.ExtractTracks(ctx, aidOri, page.Aid, page.Cid, page.Epid,
					w.Cfg.UseTvAPI, w.Cfg.UseIntlAPI, w.Cfg.UseAppAPI, firstEncoding, w.Cfg.DecryptDrm, selectedDfn)
				if err != nil {
					util.LogError("P%d 重新解析失败: %v", page.Index, err)
					return false
				}
				result = reResult
			}

			// 下载各分段并合并 (upstream: {aid}/{aid}.P{n}.{cid}.{i:pad}.mp4)
			width := len(strconv.Itoa(len(result.Clips)))
			var segFiles []string
			for i, link := range result.Clips {
				segPath := filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.%s.%s.mp4", page.Aid, page.Index, page.Cid, fmt.Sprintf("%0*d", width, i)))
				util.Log("开始下载P%d视频, 片段(%d/%d)...", page.Index, i+1, len(result.Clips))
				if err := download.DownloadFile(ctx, link, segPath, dlCfg); err != nil {
					util.LogError("P%d 片段下载失败: %v", page.Index, err)
					return false
				}
				segFiles = append(segFiles, segPath)
			}
			util.Log("下载P%d完毕", page.Index)
			util.Log("开始合并分段...")
			videoPath = filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.%s.mp4", page.Aid, page.Index, page.Cid))
			if err := muxer.MergeFLV(ctx, segFiles, videoPath); err != nil {
				util.LogError("P%d 合并分段失败: %v", page.Index, err)
				return false
			}
			// 产物校验：FLV 分段合并出来的文件就是产物（--skip-mux 时更是唯一下游就是用户）。
			if err := verifyArtifact(videoPath, "视频"); err != nil {
				util.LogError("P%d %v", page.Index, err)
				return false
			}
			if w.Cfg.SkipMux {
				if w.OnSaved != nil {
					w.OnSaved(videoPath)
				}
				return true
			}
			audioPath = "" // FLV 已包含音轨
			// 跳过下方的 dash 视频/音频下载块
			selectedVideo = nil
			selectedAudio = nil
		}

		// Video
		if selectedVideo != nil {
			videoPath = filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.%s.mp4", page.Aid, page.Index, page.Cid))
			os.MkdirAll(page.Aid, 0755)
			util.Log("开始下载P%d视频...", page.Index)
			if err := download.DownloadFile(ctx, selectedVideo.BaseURL, videoPath, withFallback(dlCfg, selectedVideo.BaseURL, origVideoURL, selectedVideo.BackupURLs...)); err != nil {
				// Per-request retries already happened inside the downloader;
				// the remaining page-level retry re-parses playurl and retries
				// the whole page (upstream retries the page body up to 3 times).
				attempt := retry + 1
				if attempt >= pageRetryLimit {
					util.LogError("P%d 视频下载失败: %v", page.Index, err)
					return false
				}
				logPageRetry(err, retryDelay)
				if !sleepCtxLocal(ctx, retryDelay) {
					return false
				}
				continue
			}
		}

		// Audio
		if selectedAudio != nil {
			audioPath = filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.%s.m4a", page.Aid, page.Index, page.Cid))
			os.MkdirAll(page.Aid, 0755)
			util.Log("开始下载P%d音频...", page.Index)
			if err := download.DownloadFile(ctx, selectedAudio.BaseURL, audioPath, withFallback(dlCfg, selectedAudio.BaseURL, origAudioURL, selectedAudio.BackupURLs...)); err != nil {
				attempt := retry + 1
				if attempt >= pageRetryLimit {
					util.LogError("P%d 音频下载失败: %v", page.Index, err)
					return false
				}
				logPageRetry(err, retryDelay)
				if !sleepCtxLocal(ctx, retryDelay) {
					return false
				}
				continue
			}
		}

		// Background audio / dubbing tracks (upstream audioMaterial).
		if !w.Cfg.VideoOnly && len(result.BackgroundAudioTracks) > 0 {
			os.MkdirAll(page.Aid, 0755)
			for i, bg := range result.BackgroundAudioTracks {
				bgPath := filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.background.%d.%s.m4a", page.Aid, page.Index, i, bg.ID))
				util.Log("开始下载背景音轨%d...", i)
				if err := download.DownloadFile(ctx, bg.BaseURL, bgPath, withFallback(dlCfg, bg.BaseURL, "", bg.BackupURLs...)); err != nil {
					util.LogWarn("背景音轨下载失败: %v", err)
					continue
				}
				backgroundMaterial = append(backgroundMaterial, entity.AudioMaterial{Path: bgPath})
			}
		}

		// 配音轨（上游对每个 role 单独夹取音频下标）：每个 role 的 audio 列表是独立的，
		// 用户选的音频序号只对主列表校验过，直接拿来索引会越界——上游为此刻意抽出
		// ClampRoleAudioIndex：越界钳到末位，列表为空则跳过该 role。此前本仓完全没下载这一路，
		// 有配音的稿件产物里会缺配音轨。
		if !w.Cfg.VideoOnly && len(result.RoleAudioList) > 0 {
			for i, role := range result.RoleAudioList {
				roleIdx := clampRoleAudioIndex(aIndex, len(role.Audio))
				if roleIdx < 0 {
					continue
				}
				roleAudio := role.Audio[roleIdx]
				// 产物路径与上游同构：<aid>/<aid>.<cid>.<净化后的 audio_id>.m4a。
				// audio_id 来自接口响应（外部输入），必须净化后再拼路径，否则 "../" 能写出工作目录（RF-18）。
				rolePath := role.Path
				if rolePath == "" {
					seg := util.SanitizePathSegment(role.AudioID)
					if seg == "" {
						seg = util.SanitizePathSegment(roleAudio.ID)
					}
					if seg == "" {
						seg = fmt.Sprintf("role%d", i)
					}
					rolePath = filepath.Join(page.Aid, fmt.Sprintf("%s.%s.%s.m4a", page.Aid, page.Cid, seg))
				}
				util.Log("开始下载P%d配音[%s]...", page.Index, role.Title)
				if err := download.DownloadFile(ctx, roleAudio.BaseURL, rolePath, withFallback(dlCfg, roleAudio.BaseURL, "", roleAudio.BackupURLs...)); err != nil {
					util.LogWarn("配音[%s]下载失败: %v", role.Title, err)
					continue
				}
				backgroundMaterial = append(backgroundMaterial, entity.AudioMaterial{
					Title:      role.Title,
					PersonName: role.PersonName,
					Path:       rolePath,
				})
			}
		}

		// 产物校验：录下来的原始轨道必须非空。混流路径至少还有 ffmpeg 兜底，但 --skip-mux
		// 保留原始轨道时它们就是最终产物，没有任何下游环节会再碰它们。
		for _, art := range []struct{ path, what string }{{videoPath, "视频"}, {audioPath, "音频"}} {
			if err := verifyArtifact(art.path, art.what); err != nil {
				util.LogError("P%d %v", page.Index, err)
				return false
			}
		}

		// DRM decryption (before mux; failure fails the page like upstream)
		if result.IsDrm && w.Cfg.DecryptDrm && (result.KidHex != "" || result.PsshBase64 != "") {
			if err := w.decryptDrm(ctx, result, videoPath, audioPath); err != nil {
				util.LogError("P%d DRM解密失败: %v", page.Index, err)
				return false
			}
		}

		// Dolby Vision with ffmpeg < 5.0: switch to mp4box (upstream).
		// --skip-mux performs no muxing at all, so the Dolby Vision probe cannot change
		// anything and must not run (upstream skips it).
		if selectedVideo != nil && selectedVideo.Dfn == config.QualityMap["126"] && !w.Cfg.UseMP4box && !w.Cfg.SkipMux && !muxer.CheckFFmpegDOVI() {
			util.LogWarn("检测到杜比视界清晰度且您的ffmpeg版本小于5.0,将使用mp4box混流...")
			w.Cfg.UseMP4box = true
		}

		// Mux or save directly
		if !w.Cfg.SkipMux && (videoPath != "" || audioPath != "") {
			if videoPath != "" && audioPath != "" {
				util.Log("开始合并音视频...")
			}
			isHevc := selectedVideo != nil && selectedVideo.Codecs == "HEVC"
			desc := vInfo.Desc
			if page.Desc != "" {
				desc = page.Desc
			}
			episodeTitle := ""
			if pagesCount > 1 || (vInfo.IsBangumi && !vInfo.IsBangumiEnd) {
				episodeTitle = page.Title
			}
			coverPath := filepath.Join(page.Aid, page.Aid+".jpg")
			if _, err := os.Stat(coverPath); err != nil {
				coverPath = ""
			}
			// Mux into a unique temporary file and move it into place only on
			// success. Writing straight to savePath left a truncated file when the
			// mux was interrupted or failed, and the "已存在, 跳过下载" check above
			// then treated that half-file as a finished download forever (upstream
			// muxes to savePath + ".muxing-{guid}.mp4" and renames).
			muxPath := fmt.Sprintf("%s.muxing-%d.mp4", savePath, time.Now().UnixNano())
			muxErr := muxer.MuxAV(ctx, w.Cfg.UseMP4box, page.Bvid(), videoPath, audioPath, muxPath,
				desc, title, page.OwnerName, episodeTitle, coverPath, lang,
				downloadedSubs, w.Cfg.AudioOnly, w.Cfg.VideoOnly, w.Cfg.SimplyMux,
				page.Points, pubTime, isHevc, backgroundMaterial, w.Cfg.MuxerTimeout)
			if muxErr != nil {
				os.Remove(muxPath)
				util.LogError("合并失败: %v", muxErr)
				return false
			}
			if err := os.Rename(muxPath, savePath); err != nil {
				os.Remove(muxPath)
				util.LogError("合并产物落盘失败: %v", err)
				return false
			}
		} else if w.Cfg.AudioOnly && audioPath != "" {
			os.Rename(audioPath, savePath)
		}

		// Cleanup temp files (skip-mux keeps the raw streams as products, upstream).
		util.Log("清理临时文件...")
		time.Sleep(200 * time.Millisecond)
		if !w.Cfg.SkipMux {
			if videoPath != "" {
				os.Remove(videoPath)
			}
			if audioPath != "" {
				os.Remove(audioPath)
			}
			// Only consumed material may be deleted: with --skip-mux nothing
			// muxed these tracks, so deleting them would discard downloaded data
			// the user asked to keep (the video/audio tracks are kept above too).
			for _, m := range backgroundMaterial {
				if m.Path != "" {
					os.Remove(m.Path)
				}
			}
		} else if w.OnSaved != nil {
			if videoPath != "" {
				w.OnSaved(videoPath)
			}
			if audioPath != "" {
				w.OnSaved(audioPath)
			}
			for _, m := range backgroundMaterial {
				if m.Path != "" {
					w.OnSaved(m.Path)
				}
			}
		}
		coverPath := filepath.Join(page.Aid, page.Aid+".jpg")
		if pagesCount == 1 || page.Index == allPages[len(allPages)-1].Index || page.Aid != allPages[len(allPages)-1].Aid {
			os.Remove(coverPath)
		}
		if dir, _ := os.ReadDir(page.Aid); len(dir) == 0 {
			os.Remove(page.Aid)
		}

		util.Log("下载P%d完毕", page.Index)

		w.writeNFOSidecar(savePath, title, page)

		if w.OnSaved != nil && savePath != "" {
			w.OnSaved(savePath)
		}

		return true
	}
	return false
}

// decryptDrm acquires DRM content keys and decrypts the downloaded streams in
// place, mirroring upstream DecryptDrmAsync semantics: any failure (missing key,
// missing mp4decrypt, decrypt error) is returned as an error so the task fails
// instead of silently delivering still-encrypted media.
func (w *Workflow) decryptDrm(ctx context.Context, result *entity.ParsedResult, videoPath, audioPath string) error {
	util.Log("检测到DRM加密，正在获取解密密钥...")

	if w.Cfg.DrmKeyHex != "" {
		result.KeyHex = w.Cfg.DrmKeyHex
	}
	if w.Cfg.DrmKidHex != "" {
		result.KidHex = w.Cfg.DrmKidHex
	}

	if result.KeyHex != "" && result.KidHex != "" {
		util.Log("使用手动提供的密钥: KEY=%s...", maskSecret(result.KeyHex))
	} else {
		if result.DrmTechType == 2 {
			if result.PsshBase64 != "" {
				wvdPath := w.Cfg.WvdPath
				if wvdPath == "" || !fileExists(wvdPath) {
					wvdPath = filepath.Join(appDirFunc(), "device.wvd")
				}
				if fileExists(wvdPath) {
					// The key window can last minutes (license request + retries); bound it
					// so serve /cancel and Ctrl+C are honoured (upstream RF-35).
					keyCtx, cancelKey := context.WithTimeout(ctx, 2*time.Minute)
					kp, err := drm.GetKeyWidevine(keyCtx, result.PsshBase64, wvdPath)
					cancelKey()
					if err != nil {
						util.LogWarn("自动密钥提取异常: %v", err)
					} else {
						result.KidHex = kp.KidHex
						result.KeyHex = kp.KeyHex
						util.Log("DRM密钥获取成功")
					}
				} else {
					util.LogWarn("Widevine DRM 需要 device.wvd 文件，请放置到程序目录")
				}
			}
		} else {
			util.LogWarn("当前DRM类型不支持自动解密，请使用 --key --kid 手动提供密钥")
		}
	}

	// mp4decrypt's key-file format is "kid:key"; both must be present, otherwise
	// decrypting would produce an invalid ":key" line or silently wrong output.
	if result.KeyHex == "" || result.KidHex == "" {
		return fmt.Errorf("DRM 解密密钥获取失败（Key 或 Kid 缺失），无法解密。请确保 device.wvd 位于程序目录，或使用 --key --kid 同时提供密钥")
	}

	mp4decrypt := drm.FindMp4decrypt(w.Cfg.Mp4decryptPath)
	if mp4decrypt == "" {
		return fmt.Errorf("未找到 mp4decrypt，无法解密 DRM 内容。请安装 Bento4 或通过 --mp4decrypt-path 指定路径")
	}

	timeout := time.Duration(w.Cfg.MuxerTimeout) * time.Minute
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	if videoPath != "" && fileExists(videoPath) {
		util.Log("解密视频流...")
		if err := drm.DecryptStream(ctx, mp4decrypt, result.KidHex, result.KeyHex, videoPath, timeout); err != nil {
			return err
		}
		util.Log("视频解密完成")
	}
	if audioPath != "" && fileExists(audioPath) {
		util.Log("解密音频流...")
		if err := drm.DecryptStream(ctx, mp4decrypt, result.KidHex, result.KeyHex, audioPath, timeout); err != nil {
			return err
		}
		util.Log("音频解密完成")
	}
	return nil
}

// fetchPointsFunc 是给用例留的接缝：fetchPoints 的 URL 硬编码 api.bilibili.com（与上游一致），
// 用例无法用假服务器拦下它，替换这个变量即可在不联网的前提下断言「-I 不抓章节」。
var fetchPointsFunc = fetchPoints

// fetchPoints fetches chapter/view points for a page (upstream FetchPointsAsync).
// Failures degrade to a warning and an empty list.
func fetchPoints(ctx context.Context, client *util.HTTPClient, cid, aid string) []entity.ViewPoint {
	var points []entity.ViewPoint
	api := fmt.Sprintf("https://api.bilibili.com/x/player/wbi/v2?cid=%s&aid=%s", cid, aid)
	resp, err := client.GetWebSource(ctx, api)
	if err != nil {
		return points
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(resp), &root); err != nil {
		util.LogWarn("获取章节信息失败 (cid=%s, aid=%s): %v", cid, aid, err)
		return points
	}
	data, ok := root["data"].(map[string]interface{})
	if !ok {
		return points
	}
	viewPoints, ok := data["view_points"].([]interface{})
	if !ok {
		return points
	}
	for _, vp := range viewPoints {
		vm, ok := vp.(map[string]interface{})
		if !ok {
			continue
		}
		points = append(points, entity.ViewPoint{
			Title: getStr(vm, "content"),
			Start: getInt(vm, "from"),
			End:   getInt(vm, "to"),
		})
	}
	return points
}

// sleepCtxLocal sleeps with context awareness (workflow-local variant).
func sleepCtxLocal(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// withFallback 给这次下载附上候选地址链（按序回退）：host 被替换前的原地址在前，该轨道从
// playurl 解析出来的 backup_url 在后（数据驱动，没有新增开关）。下载器在首个 404 或连接失败
// 时换下一个候选（本仓有意差异，见 docs/UPSTREAM_ALIGNMENT.md §4.33）。主地址本身不重复入列，
// 免得同一个地址被白白重试一次。
func withFallback(cfg download.DownloadConfig, current, original string, backups ...string) download.DownloadConfig {
	list := make([]string, 0, 1+len(backups))
	add := func(u string) {
		if u == "" || u == current {
			return
		}
		for _, seen := range list {
			if seen == u {
				return
			}
		}
		list = append(list, u)
	}
	add(original)
	for _, u := range backups {
		add(u)
	}
	cfg.FallbackURLs = list
	return cfg
}

// verifyArtifact 复核下载产物：必须存在且非空。空产物在混流路径会被 ffmpeg 拦住，但
// --skip-mux 保留原始轨道时它就是交付物——服务器返回 200 + 空体时旧行为会一路判成功，
// 用户拿到一个 0 字节的"视频"文件却看到任务完成。
func verifyArtifact(path, what string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s产物缺失: %v", what, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s产物为 0 字节（服务器返回空内容或声明长度为 0）", what)
	}
	return nil
}

// logPageRetry 打印页面级重试（上游 DownloadPageAsync 的两行：先给原因，再给
// 「X 秒后将进行自动重试...」）。轨道级重试是另一条独立阶梯、按上游口径记 Debug——
// 两级此前用同一句话同一个级别，日志读起来像「3×3=9 次连续重试」。
func logPageRetry(err error, delay time.Duration) {
	util.LogError("[%T] %v", err, err)
	util.LogWarn("下载出现异常, %v 秒后将进行自动重试...", strconv.FormatFloat(delay.Seconds(), 'f', -1, 64))
}

// getStr / getInt are small JSON helpers for map[string]interface{} data.
func getStr(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

func getInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

// maskSecret shows only the first 8 characters of a secret value.
func maskSecret(s string) string {
	n := len(s)
	if n > 8 {
		n = 8
	}
	return s[:n]
}

// fileExists reports whether the path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// initRequestSession applies loaded credentials to the HTTP client, checks the
// login state and extracts the WBI mixin key (upstream InitializeRequestSessionAsync).
func (w *Workflow) initRequestSession(ctx context.Context) {
	// The HTTP client was built with the CLI-provided cookie; switch it to the
	// effective credentials (CLI flag or BBDown.data file).
	w.HTTPClient.SetCookieFn(func() string { return w.Cfg.Cookie })
	// Warn before the login lapses: a long-running serve process would otherwise
	// start failing weeks later with no hint about the cause.
	if days := util.EstimateSessdataExpiryDays(w.Cfg.Cookie); days != nil && *days <= 7 {
		if *days <= 0 {
			util.LogWarn("本地 SESSDATA 已过期，请重新执行登录")
		} else {
			util.LogWarn("本地 SESSDATA 约 %d 天后过期，建议提前重新登录", *days)
		}
	}
	// Tell the client which hosts may receive them: the official domains, or the
	// mirrors the user opted into. A redirect target or an unconfigured host must
	// never see SESSDATA.
	w.HTTPClient.SetCredentialHosts(w.Cfg.Host, w.Cfg.EpHost, w.Cfg.TvHost)

	// Batch commands (watchlater / sub check) pre-fetch the WBI key once and
	// preset it here, avoiding one nav request per item (upstream initializes
	// the session once per command).
	if w.Cfg.Wbi != "" {
		w.sessionWbi = w.Cfg.Wbi
		return
	}

	if w.Cfg.Area == "" {
		util.Log("检测账号登录...")
		isLoggedIn, cookieExpired, newWbi := parser.CheckLoginWithDetails(ctx, w.HTTPClient, w.Cfg.Cookie)
		if newWbi != "" {
			w.sessionWbi = newWbi
		}
		if !isLoggedIn {
			if cookieExpired {
				util.LogWarn("========================================")
				util.LogWarn("  Cookie 已过期！")
				util.LogWarn("  请运行 BBDown login 重新扫码登录以获取新 Cookie。")
				util.LogWarn("  或者使用 --use-tv-api 配合 --access-token 下载。")
				util.LogWarn("  （若已执行 BBDown logintv，请加上 --use-tv-api）")
				util.LogWarn("========================================")
			} else {
				util.LogWarn("========================================")
				util.LogWarn("  你尚未登录B站账号！")
				util.LogWarn("  未登录状态下仅能下载6分钟试看片段。")
				util.LogWarn("  请运行 BBDown login 扫码登录以获取完整视频。")
				util.LogWarn("  （若已执行 BBDown logintv，请在下载命令中加上 --use-tv-api）")
				util.LogWarn("========================================")
			}
		}
	}
}

func (w *Workflow) loadCredentials() error {
	appDir := appDirFunc()
	// Find binaries
	if err := w.findBinaries(); err != nil {
		return err
	}

	// Explicit CLI credentials take precedence over local files; strip the
	// "access_token=" prefix from the CLI-provided token too (upstream LoadCredentials).
	if w.Cfg.AccessToken != "" {
		w.Cfg.AccessToken = strings.TrimPrefix(w.Cfg.AccessToken, "access_token=")
	}

	if w.Cfg.Cookie == "" {
		data, err := os.ReadFile(filepath.Join(appDir, "BBDown.data"))
		if err == nil {
			util.Log("加载本地cookie...")
			util.LogDebug("文件路径：%s", filepath.Join(appDir, "BBDown.data"))
			// 归一化字段名大小写：护照回调可能给出小写 sessdata 等字段，
			// bilibili nav 接口对大小写敏感，小写会被误判为 Cookie 已过期。
			w.Cfg.Cookie = login.NormalizeCookieNames(strings.TrimSpace(string(data)))
		}
	}
	if w.Cfg.AccessToken == "" && w.Cfg.UseTvAPI {
		data, err := os.ReadFile(filepath.Join(appDir, "BBDownTV.data"))
		if err == nil {
			util.Log("加载本地token...")
			util.LogDebug("文件路径：%s", filepath.Join(appDir, "BBDownTV.data"))
			w.Cfg.AccessToken = strings.TrimPrefix(strings.TrimSpace(string(data)), "access_token=")
		}
	}
	if w.Cfg.AccessToken == "" && w.Cfg.UseAppAPI {
		data, err := os.ReadFile(filepath.Join(appDir, "BBDownApp.data"))
		if err == nil {
			util.Log("加载本地token...")
			util.LogDebug("文件路径：%s", filepath.Join(appDir, "BBDownApp.data"))
			w.Cfg.AccessToken = strings.TrimPrefix(strings.TrimSpace(string(data)), "access_token=")
		}
	}
	return nil
}

func (w *Workflow) notifyCompletion(vInfo *entity.VInfo, success bool) {
	msg := "completed"
	if !success {
		msg = "completed-with-failures"
	}
	// Serialize with json.Marshal so titles containing quotes/control chars
	// cannot break the payload.
	body, err := json.Marshal(map[string]interface{}{
		"title":        vInfo.Title,
		"page_count":   len(vInfo.PagesInfo),
		"message":      msg,
		"completed_at": time.Now().Unix(),
	})
	if err != nil {
		return
	}
	util.LogDebug("通知回调: %s", w.Cfg.NotifyWebhook)
	w.HTTPClient.PostResponse(context.Background(), w.Cfg.NotifyWebhook, body, map[string]string{
		"Content-Type": "application/json",
	})
}

func handlePcdn(cfg *config.MyOption, video *entity.Video, audio *entity.Audio) {
	if cfg.UposHost == "" {
		if !cfg.AllowPcdn {
			if video != nil && pcdnRegex.MatchString(video.BaseURL) {
				util.LogWarn("检测到视频流为PCDN, 尝试强制替换为%s……", backupHost)
				video.BaseURL = pcdnRegex.ReplaceAllString(video.BaseURL, "://"+backupHost+"/")
			}
			if audio != nil && pcdnRegex.MatchString(audio.BaseURL) {
				util.LogWarn("检测到音频流为PCDN, 尝试强制替换为%s……", backupHost)
				audio.BaseURL = pcdnRegex.ReplaceAllString(audio.BaseURL, "://"+backupHost+"/")
			}
		}
		if cfg.Area != "" {
			if video != nil && strings.Contains(video.BaseURL, "akamaized.net") {
				util.LogWarn("检测到视频流为外国源, 尝试强制替换为%s……", backupHost)
				video.BaseURL = akamRegex.ReplaceAllString(video.BaseURL, "://"+backupHost+"/")
			}
			if audio != nil && strings.Contains(audio.BaseURL, "akamaized.net") {
				util.LogWarn("检测到音频流为外国源, 尝试强制替换为%s……", backupHost)
				audio.BaseURL = akamRegex.ReplaceAllString(audio.BaseURL, "://"+backupHost+"/")
			}
		}
	} else {
		if video != nil {
			util.LogWarn("尝试将视频流强制替换为%s……", cfg.UposHost)
			video.BaseURL = uposRegex.ReplaceAllString(video.BaseURL, "://"+cfg.UposHost+"/")
		}
		if audio != nil {
			util.LogWarn("尝试将音频流强制替换为%s……", cfg.UposHost)
			audio.BaseURL = uposRegex.ReplaceAllString(audio.BaseURL, "://"+cfg.UposHost+"/")
		}
	}
}

func appDirFunc() string {
	return util.ExecutableDir()
}

// parseEncodingPriority parses the user encoding priority (upstream: index++
// from 0 — earlier = higher priority, dedup, ToUpper, strip dashes).
func parseEncodingPriority(s string) (map[string]int, string) {
	result := make(map[string]int)
	if s == "" {
		return result, ""
	}
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "，", ",")
	s = strings.ReplaceAll(s, "-", "")
	parts := strings.Split(s, ",")
	index := 0
	first := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if first == "" {
			first = p
		}
		if _, exists := result[p]; exists {
			continue
		}
		result[p] = index
		index++
	}
	return result, first
}

// parseDfnPriority parses the quality-name priority (upstream: ToUpper, trim,
// dedup, index++ from 0).
func parseDfnPriority(s string) map[string]int {
	result := make(map[string]int)
	if s == "" {
		return result
	}
	s = strings.ReplaceAll(s, "，", ",")
	parts := strings.Split(s, ",")
	index := 0
	for _, p := range parts {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if _, exists := result[p]; exists {
			continue
		}
		result[p] = index
		index++
	}
	return result
}

// danmakuAllFormats lists the supported danmaku export formats.
var danmakuAllFormats = map[string]bool{"xml": true, "ass": true}

// parseDanmakuFormats validates the formats list (upstream: invalid names log
// an error and fall back to the defaults).
func parseDanmakuFormats(s string) ([]string, error) {
	if s == "" {
		return []string{"xml", "ass"}, nil
	}
	s = strings.ReplaceAll(s, "，", ",")
	s = strings.ToLower(s)
	var result []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !danmakuAllFormats[p] {
			util.LogError("包含不支持的下载弹幕格式：%s", s)
			return []string{"xml", "ass"}, nil
		}
		result = append(result, p)
	}
	if len(result) == 0 {
		return []string{"xml", "ass"}, nil
	}
	return result, nil
}

// applyConfig applies CLI settings to runtime (upstream ChangeWorkingDir).
func (w *Workflow) applyConfig() error {
	if w.Cfg.WorkDir != "" {
		// Expand environment variables, resolve to a full path and create it.
		dir := os.ExpandEnv(w.Cfg.WorkDir)
		full, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("工作目录无效: %w", err)
		}
		if err := os.MkdirAll(full, 0o755); err != nil {
			return fmt.Errorf("创建工作目录失败: %w", err)
		}
		if err := os.Chdir(full); err != nil {
			return fmt.Errorf("切换工作目录失败: %w", err)
		}
		util.LogDebug("切换工作目录至：%s", full)
	}
	if w.Cfg.UserAgent != "" {
		w.HTTPClient.SetUserAgent(w.Cfg.UserAgent)
	}
	return nil
}

// handleConflictingOptions resolves mutually exclusive CLI options.
func (w *Workflow) handleConflictingOptions() {
	if w.Cfg.Interactive {
		w.Cfg.HideStreams = false
	}
	if w.Cfg.AudioOnly && w.Cfg.VideoOnly {
		w.Cfg.AudioOnly = false
		w.Cfg.VideoOnly = false
	}
	if w.Cfg.SkipSubtitle {
		w.Cfg.SubOnly = false
	}
}

// clampRoleAudioIndex 把用户选中的音频序号夹到某个 role 自己的音频列表范围内
// （上游 Download.ClampRoleAudioIndex）：列表为空返回 -1 表示跳过该 role；
// 越界钳到末位而不是报错——每个 role 的清晰度数量与主音频列表无关。
func clampRoleAudioIndex(aIndex, audioCount int) int {
	if audioCount <= 0 {
		return -1
	}
	if aIndex < 0 {
		return 0
	}
	if aIndex > audioCount-1 {
		return audioCount - 1
	}
	return aIndex
}

// shouldSkipProduct 是「产物已存在 → 跳过下载」这条判定的**唯一真源**（默认 = 上游语义）；
// `--overwrite` 在这里短路。抽成函数是为了让判定能被确定性单测，而不是在用例里复制一份逻辑。
func shouldSkipProduct(overwrite bool, savePath string) bool {
	return !overwrite && skipExistingProduct(savePath)
}

// skipExistingProduct 判断产物是否已存在且可用（非空）。
func skipExistingProduct(savePath string) bool {
	info, err := os.Stat(savePath)
	return err == nil && info.Size() > 0
}

// writeNFOSidecar 在产物旁写同名 .nfo（Kodi/Emby/Jellyfin 扫库用）：--nfo 未开、产物不存在、或写入失败
// 都只是「不写/告警」，绝不影响下载结果本身。
func (w *Workflow) writeNFOSidecar(savePath, title string, page entity.Page) {
	if !w.Cfg.WriteNFO || savePath == "" {
		return
	}
	if _, err := os.Stat(savePath); err != nil {
		return // 没有产物就没有可描述的元数据（如 --skip-mux / 只下字幕）
	}
	body, err := download.RenderNFO(title, page.Title, page.OwnerName, page.Bvid(), page.Index, page.PubTime)
	if err != nil {
		util.LogWarn("生成 NFO 失败: %v", err)
		return
	}
	if err := os.WriteFile(savePath+".nfo", []byte(body), 0o644); err != nil {
		util.LogWarn("写入 NFO 失败: %v", err)
		return
	}
	util.LogDebug("已写入侧车元数据: %s.nfo", savePath)
}

// validateNumericOptions rejects out-of-range numeric options (upstream
// throws ArgumentException with the same bounds).
func (w *Workflow) validateNumericOptions() error {
	const maxMuxerTimeoutMinutes = 35000
	if w.Cfg.MuxerTimeout < 1 || w.Cfg.MuxerTimeout > maxMuxerTimeoutMinutes {
		return fmt.Errorf("参数有误：--muxer-timeout 需在 1 ~ %d 分钟之间，当前为 %d", maxMuxerTimeoutMinutes, w.Cfg.MuxerTimeout)
	}
	if w.Cfg.RetryCount < 1 || w.Cfg.RetryCount > 100 {
		return fmt.Errorf("参数有误：--retry-count 需在 1 ~ 100 之间，当前为 %d（设为 0 将不会发起任何下载，过大则无限重试拖垮任务）", w.Cfg.RetryCount)
	}
	if w.Cfg.RetryDelay < 0 || w.Cfg.RetryDelay > 600000 {
		return fmt.Errorf("参数有误：--retry-delay 需在 0 ~ 600000 ms 之间，当前为 %d", w.Cfg.RetryDelay)
	}
	// 0 是**合法**值：表示「自动分片」（按并发数倒推，见 download.planSegmentBytes）。
	// 这条校验曾只允许 1~1024，而 CLI 的默认值在 O4 里改成了 0 —— 于是从 1.6.20-go.3 起**默认参数
	// 直接失败**，单元测试却全绿（没有任何用例走过「CLI 默认值 → 校验」这条路）。补了用例见 §4.42。
	if w.Cfg.ThreadSegmentSize < 0 || w.Cfg.ThreadSegmentSize > 1024 {
		return fmt.Errorf("参数有误：--thread-segment-size 需在 0 ~ 1024 MB 之间，当前为 %d（0 = 自动：按并发数倒推分片大小）", w.Cfg.ThreadSegmentSize)
	}
	if w.Cfg.DelayPerPage < 0 || w.Cfg.DelayPerPage > 600 {
		return fmt.Errorf("参数有误：--delay-per-page 需在 0 ~ 600 秒之间，当前为 %d", w.Cfg.DelayPerPage)
	}
	return nil
}

// printVideoHeader 打印稿件头部信息，标签与时区格式对齐上游 Workflow.cs：
// 「视频标题: 」「发布时间: 」「视频URL: 」「UP主页: 」。此前标题与 URL 都是裸值，
// 用户分不清哪一行是什么，脚本也无法按标签取值。
func printVideoHeader(vInfo *entity.VInfo, useIntlAPI bool) {
	util.LogColor("视频标题: %s", vInfo.Title)
	if vInfo.PubTime > 0 {
		// 上游 FormatTimeStamp(pubTime, "yyyy-MM-dd HH:mm:ss zzz")：带本地时区偏移。
		util.Log("发布时间: %s", time.Unix(vInfo.PubTime, 0).Format("2006-01-02 15:04:05 -07:00"))
	}
	if len(vInfo.PagesInfo) > 0 {
		if bvid := vInfo.PagesInfo[0].Bvid(); bvid != "" && !useIntlAPI {
			util.Log("视频URL: https://www.bilibili.com/video/%s/", bvid)
		}
	}
	for _, p := range vInfo.PagesInfo {
		if p.OwnerMid != "" {
			util.Log("UP主页: https://space.bilibili.com/%s", p.OwnerMid)
			break
		}
	}
}

// applySteinGateFallback 处理「互动视频不支持 TV 端下载」（上游 Workflow.cs:156-160）：
// 用户加了 -t 又碰到互动视频时，上游打印提示后就地改回默认（WEB）解析——
// TV 接口拿不到互动视频的分P，继续走 TV 只会失败。
func applySteinGateFallback(cfg *config.MyOption, vInfo *entity.VInfo) {
	if vInfo.IsSteinGate && cfg.UseTvAPI {
		util.Log("视频为互动视频，暂时不支持tv下载，修改为默认下载")
		cfg.UseTvAPI = false
	}
}

// maxExpandedPages caps -p range expansion (upstream MaxExpandedPages).
const maxExpandedPages = 100000

// getSelectedPages returns selected page indices, or nil for all. Parse errors
// abort the task (upstream rethrows; silently falling back to ALL was a bug).
func getSelectedPages(cfg *config.MyOption, vInfo *entity.VInfo, input string) ([]string, error) {
	sel := strings.ToUpper(strings.TrimSpace(cfg.SelectPage))
	sel = strings.Trim(sel, ",")
	if sel == "" {
		// Auto-select from VInfo index or URL query param（上游 Pages.cs:22-33）。
		// 两种来源都要给出提示：否则用户看到「已选择: 3」却不知道这是程序按 URL 里的
		// ?p=3 自动选的，误以为丢了其他分P。
		const autoSelectNotice = "程序已自动选择你输入的集数, 如果要下载其他集数请自行指定分P(如可使用-p ALL代表全部)"
		if vInfo.Index != "" {
			util.Log("%s", autoSelectNotice)
			return []string{vInfo.Index}, nil
		}
		if p := queryParam(input, "p"); p != "" {
			util.Log("%s", autoSelectNotice)
			return []string{p}, nil
		}
		return nil, nil // ALL
	}

	if sel == "ALL" {
		return nil, nil
	}

	sel = expandPageAliases(sel, len(vInfo.PagesInfo))

	result, err := parsePageSelection(sel)
	if err != nil {
		util.LogError("解析分P选择失败: %v", err)
		return nil, err
	}
	return result, nil
}

// expandPageAliases 把「最新分P」别名（LAST/NEW/LATEST）按**逗号分段、整段全词**展开为实际页数。
//
// 不能做子串替换——上游为此专门踩过坑：LAST 是 LATEST 的前缀，先替换 LAST 会把 -p LATEST
// 变成 "<N>EST"，轻则报「所选分P不存在: 5EST」，重则 -p 3,LATEST 里的 5EST 被静默丢弃，
// 用户以为下全了其实只下了 P3。
func expandPageAliases(selectPage string, pageCount int) string {
	last := fmt.Sprintf("%d", pageCount)
	expand := func(text string) string {
		trimmed := strings.TrimSpace(text)
		switch strings.ToUpper(trimmed) {
		case "LAST", "NEW", "LATEST":
			return last
		}
		return trimmed
	}
	segments := strings.Split(selectPage, ",")
	for i, seg := range segments {
		trimmed := strings.TrimSpace(seg)
		// 连字符从第 2 个字符找起："-5" 这种负数不是范围（与 parsePageSelection 同规则）。
		if len(trimmed) > 1 {
			if rel := strings.Index(trimmed[1:], "-"); rel >= 0 {
				dash := rel + 1
				segments[i] = expand(trimmed[:dash]) + "-" + expand(trimmed[dash+1:])
				continue
			}
		}
		segments[i] = expand(trimmed)
	}
	return strings.Join(segments, ",")
}

// parsePageSelection parses expressions like "1,3,5", "1-10", "1-3,7,9-11".
// Invalid input is an error (upstream throws; never falls back to ALL).
func parsePageSelection(expr string) ([]string, error) {
	parts := strings.Split(expr, ",")
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Range detection skips the first char so "-5" is treated as a single
		// (invalid) token instead of a range (upstream IndexOf('-', 1)).
		dashIdx := strings.Index(part[1:], "-")
		if dashIdx < 0 {
			n, err := strconv.Atoi(part)
			// Reject negatives/non-numeric ("-5" upstream parsed as a token and
			// only failed later with a confusing error; a clear message is the
			// same effect for invalid input).
			if err != nil || n < 1 {
				return nil, fmt.Errorf("无法识别的分P %q", part)
			}
			result = append(result, part)
			continue
		}
		// 连字符两侧允许空白：上游用 int.TryParse，它容忍两侧空白，
		// 所以 "1 - 3" 是合法范围；此前用 Sscanf("%d-%d") 解析会直接报错。
		dash := dashIdx + 1
		s, errS := strconv.Atoi(strings.TrimSpace(part[:dash]))
		e, errE := strconv.Atoi(strings.TrimSpace(part[dash+1:]))
		if errS != nil || errE != nil || s < 1 || e < 1 {
			return nil, fmt.Errorf("无法识别的分P范围 %q", part)
		}
		if s > e {
			return nil, fmt.Errorf("分P范围 %q 的起始值大于结束值", part)
		}
		// Cumulative, not per segment: "1-60000,1-60000" used to pass the per-range
		// check twice and expand to 120000 entries (a serve-side memory/CPU
		// amplification).
		// 单段上限与累计上限分开报错（上游 MaxExpandedPages 的两种措辞），便于用户区分。
		if e-s+1 > maxExpandedPages {
			return nil, fmt.Errorf("分P范围 %q 展开后超过 %d 项", part, maxExpandedPages)
		}
		if len(result)+e-s+1 > maxExpandedPages {
			return nil, fmt.Errorf("分P选择表达式展开后总量超过 %d 项", maxExpandedPages)
		}
		for i := s; i <= e; i++ {
			result = append(result, strconv.Itoa(i))
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("未选中任何分P")
	}
	return result, nil
}

// findBinaries locates external tools (upstream FindBinaries: explicit paths,
// then PATH lookup; missing muxer/aria2c binaries abort the task).
func (w *Workflow) findBinaries() error {
	// Use user-specified paths if set
	if w.Cfg.FFmpegPath != "" {
		if info, err := os.Stat(w.Cfg.FFmpegPath); err == nil && !info.IsDir() {
			muxer.FFMPEG = w.Cfg.FFmpegPath
		}
	}
	if w.Cfg.Mp4boxPath != "" {
		if info, err := os.Stat(w.Cfg.Mp4boxPath); err == nil && !info.IsDir() {
			muxer.MP4BOX = w.Cfg.Mp4boxPath
		}
	}

	// Find ffmpeg or mp4box when muxing is needed.
	if !w.Cfg.SkipMux {
		if w.Cfg.UseMP4box {
			if _, err := os.Stat(muxer.MP4BOX); err != nil {
				p, err := exec.LookPath("mp4box")
				if err != nil {
					p, err = exec.LookPath("MP4Box")
				}
				if err != nil {
					return fmt.Errorf("找不到可执行的mp4box文件")
				}
				muxer.MP4BOX = p
			}
		} else if _, err := os.Stat(muxer.FFMPEG); err != nil {
			p, err := exec.LookPath("ffmpeg")
			if err != nil {
				return fmt.Errorf("找不到可执行的ffmpeg文件")
			}
			muxer.FFMPEG = p
		}
	}

	// Find aria2c when requested.
	if w.Cfg.UseAria2c {
		if w.Cfg.Aria2cPath == "" {
			p, err := exec.LookPath("aria2c")
			if err != nil {
				return fmt.Errorf("找不到可执行的aria2c文件")
			}
			w.Cfg.Aria2cPath = p
		} else if info, err := os.Stat(w.Cfg.Aria2cPath); err != nil || info.IsDir() {
			return fmt.Errorf("找不到可执行的aria2c文件")
		}
	}
	return nil
}

// resolveSavePathFormat 选择单P/多P模板（上游 PathHelper.ResolveSavePathFormat）：
// 实际分P数 >1 或需要多P模板时用 multiFilePattern（空则用默认多P模板），
// 否则用 filePattern（空则用默认单P模板）。
func resolveSavePathFormat(filePattern, multiFilePattern string, actualPageCount int, useMultiWhenSingle bool) string {
	const singlePageDefault = "<videoTitle>"
	const multiPageDefault = "<videoTitle>/[P<pageNumberWithZero>]<pageTitle>"

	single := filePattern
	if single == "" {
		single = singlePageDefault
	}
	if actualPageCount > 1 || useMultiWhenSingle {
		if multiFilePattern != "" {
			return multiFilePattern
		}
		return multiPageDefault
	}
	return single
}

// handleDeprecatedOptions maps deprecated flags to their replacements (upstream).
func (w *Workflow) handleDeprecatedOptions() {
	const singlePageDefault = "<videoTitle>"
	const multiPageDefault = "<videoTitle>/[P<pageNumberWithZero>]<pageTitle>"

	if w.Cfg.AddDfnSuffix {
		util.LogWarn("--add-dfn-subfix 已被弃用, 建议使用 --file-pattern/-F 或 --multi-file-pattern/-M 来自定义输出文件名格式")
		if w.Cfg.FilePattern == "" && w.Cfg.MultiFilePattern == "" {
			if w.Cfg.FilePattern == "" {
				w.Cfg.FilePattern = singlePageDefault + "[<dfn>]"
			}
			if w.Cfg.MultiFilePattern == "" {
				w.Cfg.MultiFilePattern = multiPageDefault + "[<dfn>]"
			}
			util.LogWarn("已切换至 -F %q -M %q", w.Cfg.FilePattern, w.Cfg.MultiFilePattern)
		}
	}
	if w.Cfg.Aria2cProxy != "" {
		util.LogWarn("--aria2c-proxy 已被弃用, 请使用 --aria2c-args 来设置aria2c代理, 本次执行已添加该代理")
		w.Cfg.Aria2cArgs += " --all-proxy=" + strconv.Quote(w.Cfg.Aria2cProxy)
	}
	if w.Cfg.OnlyHevc {
		util.LogWarn("--only-hevc/-hevc 已被弃用, 请使用 --encoding-priority 来设置编码优先级, 本次执行已将hevc设置为最高优先级")
		w.Cfg.EncodingPriority = "hevc"
	}
	if w.Cfg.OnlyAvc {
		util.LogWarn("--only-avc/-avc 已被弃用, 请使用 --encoding-priority 来设置编码优先级, 本次执行已将avc设置为最高优先级")
		w.Cfg.EncodingPriority = "avc"
	}
	if w.Cfg.OnlyAv1 {
		util.LogWarn("--only-av1/-av1 已被弃用, 请使用 --encoding-priority 来设置编码优先级, 本次执行已将av1设置为最高优先级")
		w.Cfg.EncodingPriority = "av1"
	}
	if w.Cfg.NoPaddingPageNum {
		util.LogWarn("--no-padding-page-num 已被弃用, 建议使用 --file-pattern/-F 或 --multi-file-pattern/-M 来自定义输出文件名格式")
		if w.Cfg.FilePattern == "" && w.Cfg.MultiFilePattern == "" {
			w.Cfg.MultiFilePattern = strings.ReplaceAll(multiPageDefault, "<pageNumberWithZero>", "<pageNumber>")
			util.LogWarn("已切换至 -M %q", w.Cfg.MultiFilePattern)
		}
	}
	if w.Cfg.BandwidthAscending {
		util.LogWarn("--bandwith-ascending 已被弃用, 建议使用 --video-ascending 与 --audio-ascending 来指定视频或音频是否升序, 本次执行已将视频与音频均设为升序")
		w.Cfg.VideoAscending = true
		w.Cfg.AudioAscending = true
	}
}

// checkAidArchived checks if an aid has been downloaded before.
// ArchiveTracker decides when an aid may be recorded in BBDown.archives.
// Archiving is per aid, but a multi-page video must only be archived once every
// page has succeeded: recording right after the first page made the remaining
// pages of the same video look "already downloaded" on the next run and skip
// them (upstream RF-75 ArchiveTracker).
type ArchiveTracker struct {
	pending map[string]int
	failed  map[string]bool
}

// NewArchiveTracker takes the aids of every page about to be processed.
func NewArchiveTracker(aids []string) *ArchiveTracker {
	t := &ArchiveTracker{pending: map[string]int{}, failed: map[string]bool{}}
	for _, a := range aids {
		t.pending[a]++
	}
	return t
}

// OnProcessed reports whether aid may now be archived. A single failure for an
// aid disqualifies it for the whole run.
func (t *ArchiveTracker) OnProcessed(aid string, ok bool) bool {
	if !ok {
		t.failed[aid] = true
	}
	if t.pending[aid] > 0 {
		t.pending[aid]--
	}
	return t.pending[aid] == 0 && !t.failed[aid]
}

// pageLock guards one page's output for the whole attempt sequence. Acquiring it
// per attempt deadlocked: the lock is held until the page finishes, so a retry
// (which runs in the same goroutine after a failed download) blocked forever on
// a lock it already owned. sync.Mutex is not reentrant, and a mutex wait does not
// observe context cancellation — which is why Ctrl+C could not interrupt it.
type pageLock struct{ release func() }

func (p *pageLock) acquire(path string) {
	if p.release == nil && path != "" {
		p.release = lockPath(path)
	}
}

func (p *pageLock) unlock() {
	if p.release != nil {
		p.release()
		p.release = nil
	}
}

// pathLocks serialise downloads that target the same product. Two concurrent
// tasks (serve tasks, or a batch command) would otherwise both pass the
// "already exists" check, download the same media twice, and have the loser
// overwrite the winner's finished file.
var (
	pathLocksMu sync.Mutex
	pathLocks   = map[string]*pathLock{}
)

type pathLock struct {
	mu   sync.Mutex
	refs int
}

// lockPath takes the lock for path and returns the release function. Unrelated
// paths never contend, and the entry is dropped once the last holder leaves.
func lockPath(path string) func() {
	pathLocksMu.Lock()
	l, ok := pathLocks[path]
	if !ok {
		l = &pathLock{}
		pathLocks[path] = l
	}
	l.refs++
	pathLocksMu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		pathLocksMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(pathLocks, path)
		}
		pathLocksMu.Unlock()
	}
}

func (w *Workflow) checkAidArchived(aid string) bool {
	if !w.Cfg.SaveArchivesToFile {
		return false
	}
	data, err := os.ReadFile(filepath.Join(appDirFunc(), "BBDown.archives"))
	if err != nil {
		return false
	}
	for _, item := range strings.Split(string(data), "|") {
		if item == aid {
			return true
		}
	}
	return false
}

// saveAidArchived records an aid as downloaded.
func (w *Workflow) saveAidArchived(aid string) {
	if !w.Cfg.SaveArchivesToFile {
		return
	}
	f, err := os.OpenFile(filepath.Join(appDirFunc(), "BBDown.archives"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s|", aid)
}

// stdinReader is os.Stdin in production; tests can swap it.
var stdinReader io.Reader = os.Stdin

// readIntSafe reads an integer from stdin. Returns ok=false when the context
// is cancelled (e.g. Ctrl+C): the caller must abort instead of silently
// defaulting to 0 and continuing the download.
func readIntSafe(ctx context.Context) (int, bool) {
	ch := make(chan int, 1)
	go func() {
		var v int
		fmt.Fscanf(stdinReader, "%d", &v)
		ch <- v
	}()
	select {
	case <-ctx.Done():
		return 0, false
	case v := <-ch:
		return v, true
	}
}
