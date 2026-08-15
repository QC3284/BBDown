package workflow

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
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

	// Print metadata
	util.LogColor("%s", vInfo.Title)
	if vInfo.PubTime > 0 {
		t := time.Unix(vInfo.PubTime, 0)
		util.Log("发布时间: %s", t.Format("2006-01-02 15:04:05"))
	}
	if len(vInfo.PagesInfo) > 0 && vInfo.PagesInfo[0].Bvid() != "" {
		util.Log("https://www.bilibili.com/video/%s", vInfo.PagesInfo[0].Bvid())
	}

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
	savePathFormat := w.Cfg.FilePattern
	if savePathFormat == "" {
		savePathFormat = "<videoTitle>"
	}
	if pagesCount > 1 || (bangumi && !vInfo.IsBangumiEnd) {
		if w.Cfg.MultiFilePattern != "" {
			savePathFormat = w.Cfg.MultiFilePattern
		} else {
			savePathFormat = "<videoTitle>/[P<pageNumberWithZero>]<pageTitle>"
		}
	}

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
			failedPages = append(failedPages, page.Index)
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

	// Page-level retry is fixed at 3 (upstream); per-request retries live in the
	// downloader and honor --retry-count/--retry-delay.
	const pageRetryLimit = 3
	retryDelay := time.Duration(w.Cfg.RetryDelay) * time.Millisecond
	if retryDelay <= 0 {
		retryDelay = 3 * time.Second
	}
	for retry := 0; retry < pageRetryLimit; retry++ {
		// Fetch chapter/view points (upstream FetchPointsAsync; failure degrades
		// to a warning and an empty chapter list).
		page.Points = fetchPoints(ctx, w.HTTPClient, page.Cid, page.Aid)

		// Parse tracks
		result, err := p.ExtractTracks(ctx, aidOri, page.Aid, page.Cid, page.Epid,
			w.Cfg.UseTvAPI, w.Cfg.UseIntlAPI, w.Cfg.UseAppAPI, firstEncoding, w.Cfg.DecryptDrm, "")
		if err != nil {
			attempt := retry + 1
			if attempt >= pageRetryLimit {
				util.LogError("P%d 解析失败（重试%d次后）: %v", page.Index, attempt, err)
				return false
			}
			util.LogWarn("解析异常, %v 后重试... (%d/%d)", retryDelay, attempt, pageRetryLimit)
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
		vIndex := 0
		aIndex := 0

		if w.Cfg.Interactive {
			if len(result.VideoTracks) > 0 {
				fmt.Print("请选择一条视频流(输入序号): ")
				fmt.Print("\033[36m")
				vIndex = readIntSafe(ctx)
				fmt.Print("\033[0m")
				if vIndex < 0 || vIndex >= len(result.VideoTracks) {
					vIndex = 0
				}
			}
			if len(result.AudioTracks) > 0 {
				fmt.Print("请选择一条音频流(输入序号): ")
				fmt.Print("\033[36m")
				aIndex = readIntSafe(ctx)
				fmt.Print("\033[0m")
				if aIndex < 0 || aIndex >= len(result.AudioTracks) {
					aIndex = 0
				}
			}
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
		handlePcdn(&w.Cfg, selectedVideo, selectedAudio)

		util.Log("已选择的流:")
		download.PrintSelectedTrack(selectedVideo, selectedAudio, page.Dur)

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

		// Skip if exists
		if info, err := os.Stat(savePath); err == nil && info.Size() > 0 {
			util.Log("%s 已存在, 跳过下载...", savePath)
			return true
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

		// Cover-only: download cover with output naming (matching C#: no early return)
		if w.Cfg.CoverOnly {
			coverURL := pic
			if coverURL == "" {
				coverURL = page.Cover
			}
			if coverURL != "" {
				coverExt := filepath.Ext(coverURL)
				if idx := strings.Index(coverExt, "?"); idx >= 0 {
					coverExt = coverExt[:idx]
				}
				newCover := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + coverExt
				os.MkdirAll(filepath.Dir(newCover), 0755)
				if err := download.DownloadFile(ctx, coverURL, newCover, dlCfg); err != nil {
					util.LogWarn("封面下载失败: %v", err)
				}
			}
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
						outPath = strings.TrimSuffix(outPath, filepath.Ext(outPath)) + "." + s.Lan + ".srt"
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
			if w.Cfg.Interactive {
				for i, q := range result.Dfns {
					util.LogColorNoTime("%d.%s", i, config.QualityMap[q])
				}
				fmt.Print("请选择最想要的清晰度(输入序号): ")
				fmt.Print("\033[36m")
				qi := readIntSafe(ctx)
				fmt.Print("\033[0m")
				if qi >= len(result.Dfns) || qi < 0 {
					qi = 0
				}
				reResult, err := p.ExtractTracks(ctx, aidOri, page.Aid, page.Cid, page.Epid,
					w.Cfg.UseTvAPI, w.Cfg.UseIntlAPI, w.Cfg.UseAppAPI, firstEncoding, w.Cfg.DecryptDrm, result.Dfns[qi])
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
			if err := download.DownloadFile(ctx, selectedVideo.BaseURL, videoPath, dlCfg); err != nil {
				// Per-request retries already happened inside the downloader;
				// the remaining page-level retry re-parses playurl and retries
				// the whole page (upstream retries the page body up to 3 times).
				attempt := retry + 1
				if attempt >= pageRetryLimit {
					util.LogError("P%d 视频下载失败: %v", page.Index, err)
					return false
				}
				util.LogWarn("下载异常, %v 后重试... (%d/%d)", retryDelay, attempt, pageRetryLimit)
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
			if err := download.DownloadFile(ctx, selectedAudio.BaseURL, audioPath, dlCfg); err != nil {
				attempt := retry + 1
				if attempt >= pageRetryLimit {
					util.LogError("P%d 音频下载失败: %v", page.Index, err)
					return false
				}
				util.LogWarn("下载异常, %v 后重试... (%d/%d)", retryDelay, attempt, pageRetryLimit)
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
				if err := download.DownloadFile(ctx, bg.BaseURL, bgPath, dlCfg); err != nil {
					util.LogWarn("背景音轨下载失败: %v", err)
					continue
				}
				backgroundMaterial = append(backgroundMaterial, entity.AudioMaterial{Path: bgPath})
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
		if selectedVideo != nil && selectedVideo.Dfn == config.QualityMap["126"] && !w.Cfg.UseMP4box && !muxer.CheckFFmpegDOVI() {
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
			if err := muxer.MuxAV(ctx, w.Cfg.UseMP4box, page.Bvid(), videoPath, audioPath, savePath,
				desc, title, page.OwnerName, episodeTitle, coverPath, lang,
				downloadedSubs, w.Cfg.AudioOnly, w.Cfg.VideoOnly, w.Cfg.SimplyMux,
				page.Points, pubTime, isHevc, backgroundMaterial, w.Cfg.MuxerTimeout); err != nil {
				util.LogError("合并失败: %v", err)
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
		} else if w.OnSaved != nil {
			if videoPath != "" {
				w.OnSaved(videoPath)
			}
			if audioPath != "" {
				w.OnSaved(audioPath)
			}
		}
		for _, m := range backgroundMaterial {
			os.Remove(m.Path)
		}
		coverPath := filepath.Join(page.Aid, page.Aid+".jpg")
		if pagesCount == 1 || page.Index == allPages[len(allPages)-1].Index || page.Aid != allPages[len(allPages)-1].Aid {
			os.Remove(coverPath)
		}
		if dir, _ := os.ReadDir(page.Aid); len(dir) == 0 {
			os.Remove(page.Aid)
		}

		util.Log("下载P%d完毕", page.Index)

		w.saveAidArchived(page.Aid)

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
					kp, err := drm.GetKeyWidevine(result.PsshBase64, wvdPath)
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
			w.Cfg.Cookie = strings.TrimSpace(string(data))
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
	if w.Cfg.ThreadSegmentSize < 1 || w.Cfg.ThreadSegmentSize > 1024 {
		return fmt.Errorf("参数有误：--thread-segment-size 需在 1 ~ 1024 MB 之间，当前为 %d（设为 0 会导致分片切分无法收敛）", w.Cfg.ThreadSegmentSize)
	}
	if w.Cfg.DelayPerPage < 0 || w.Cfg.DelayPerPage > 600 {
		return fmt.Errorf("参数有误：--delay-per-page 需在 0 ~ 600 秒之间，当前为 %d", w.Cfg.DelayPerPage)
	}
	return nil
}

// maxExpandedPages caps -p range expansion (upstream MaxExpandedPages).
const maxExpandedPages = 100000

// getSelectedPages returns selected page indices, or nil for all. Parse errors
// abort the task (upstream rethrows; silently falling back to ALL was a bug).
func getSelectedPages(cfg *config.MyOption, vInfo *entity.VInfo, input string) ([]string, error) {
	sel := strings.ToUpper(strings.TrimSpace(cfg.SelectPage))
	sel = strings.Trim(sel, ",")
	if sel == "" {
		// Auto-select from VInfo index or URL query param
		if vInfo.Index != "" {
			return []string{vInfo.Index}, nil
		}
		if idx := strings.Index(input, "?p="); idx >= 0 {
			p := input[idx+3:]
			if end := strings.IndexAny(p, "& "); end >= 0 {
				p = p[:end]
			}
			return []string{p}, nil
		}
		return nil, nil // ALL
	}

	if sel == "ALL" {
		return nil, nil
	}

	// Replace LAST/NEW/LATEST with the page count (upstream uses Count).
	lastIdx := fmt.Sprintf("%d", len(vInfo.PagesInfo))
	sel = strings.ReplaceAll(sel, "LAST", lastIdx)
	sel = strings.ReplaceAll(sel, "NEW", lastIdx)
	sel = strings.ReplaceAll(sel, "LATEST", lastIdx)

	result, err := parsePageSelection(sel)
	if err != nil {
		util.LogError("解析分P选择失败: %v", err)
		return nil, err
	}
	return result, nil
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
				return nil, fmt.Errorf("无法识别的分P范围 %q", part)
			}
			result = append(result, part)
			continue
		}
		var s, e int
		if _, err := fmt.Sscanf(part, "%d-%d", &s, &e); err != nil || fmt.Sprintf("%d-%d", s, e) != part {
			return nil, fmt.Errorf("无法识别的分P范围 %q", part)
		}
		if s < 1 || e < 1 {
			return nil, fmt.Errorf("无法识别的分P范围 %q", part)
		}
		if s > e {
			return nil, fmt.Errorf("起始值大于结束值: %q", part)
		}
		if e-s+1 > maxExpandedPages {
			return nil, fmt.Errorf("展开后超过 %d 项: %q", maxExpandedPages, part)
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

// readIntSafe reads an integer from stdin, respecting context cancellation.
func readIntSafe(ctx context.Context) int {
	ch := make(chan int, 1)
	go func() {
		var v int
		fmt.Scanf("%d", &v)
		ch <- v
	}()
	select {
	case <-ctx.Done():
		return 0
	case v := <-ch:
		return v
	}
}
