package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/fetcher"
	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/parser"
	"github.com/QC3284/BBDown/internal/util"
)

const backupHost = "upos-sz-mirrorcoso1.bilivideo.com"

// Workflow orchestrates the BBDown download process.
type Workflow struct {
	Cfg        config.MyOption
	HTTPClient *util.HTTPClient
}

// New creates a new Workflow.
func New(cfg config.MyOption, client *util.HTTPClient) *Workflow {
	return &Workflow{Cfg: cfg, HTTPClient: client}
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
	util.Log("解析完成: %s", aidOri)

	// Load credentials from data files
	w.loadCredentials()

	// Parse priorities
	encodingPriority, firstEncoding := parseEncodingPriority(w.Cfg.EncodingPriority)
	dfnPriority := parseDfnPriority(w.Cfg.DfnPriority)
	lang := w.Cfg.Language
	delay := w.Cfg.DelayPerPage

	// Fetch video info
	factory := fetcher.NewFactory(w.HTTPClient, w.Cfg.UseIntlAPI)
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

	// Page summary
	pagesInfo := vInfo.PagesInfo
	util.Log("共计 %d 个分P, 已选择：ALL", len(pagesInfo))
	showPages := pagesInfo
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
		UseAria2c:   w.Cfg.UseAria2c,
		Aria2cArgs:  w.Cfg.Aria2cArgs,
		ForceHTTP:   w.Cfg.ForceHTTP,
		MultiThread: w.Cfg.MultiThread,
	}

	parserInst := parser.NewParser(w.HTTPClient, config.Current(nil))

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

		success := w.downloadOnePage(ctx, parserInst, page, vInfo, pagesInfo, aidOri, savePathFormat, apiType,
			encodingPriority, dfnPriority, firstEncoding, lang, dlCfg)
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
	dlCfg download.DownloadConfig) bool {

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

	maxRetries := 3
	for retry := 0; retry < maxRetries; retry++ {
		// Parse tracks
		result, err := p.ExtractTracks(ctx, aidOri, page.Aid, page.Cid, page.Epid,
			w.Cfg.UseTvAPI, w.Cfg.UseIntlAPI, w.Cfg.UseAppAPI, firstEncoding, w.Cfg.DecryptDrm, "")
		if err != nil {
			retry := retry + 1
			if retry >= maxRetries {
				util.LogError("P%d 解析失败（重试%d次后）: %v", page.Index, retry, err)
				return false
			}
			util.LogWarn("解析异常, 3秒后重试... (%d/%d)", retry, maxRetries)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(3 * time.Second):
			}
			continue
		}

		// Sort
		result.VideoTracks = download.SortVideoTracks(result.VideoTracks, dfnPriority, encodingPriority, w.Cfg.VideoAscending)
		result.AudioTracks = download.SortAudioTracks(result.AudioTracks, encodingPriority, w.Cfg.AudioAscending)

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
			// 交互式选择视频流
			for i, v := range result.VideoTracks {
				kbps := float64(0)
				if v.Dur > 0 && v.Size > 0 {
					kbps = v.Size / 1024 / float64(v.Dur) * 8
				}
				line := fmt.Sprintf("%d. [%s] [%s] [%s] [%s] [~%02.0f kbps] [%s]",
					i, v.Dfn, v.Res, v.Codecs, v.FPS, kbps, util.FormatFileSize(v.Size))
				line = strings.ReplaceAll(line, "[] ", "")
				util.LogColorNoTime("%s", line)
			}
			fmt.Print("请选择最想要的视频流(输入序号): ")
			fmt.Scanf("%d", &vIndex)
			if vIndex < 0 || vIndex >= len(result.VideoTracks) {
				vIndex = 0
			}

			// 交互式选择音频流
			for i, a := range result.AudioTracks {
				line := fmt.Sprintf("%d. [%s] [%s] [~%d kbps]", i, a.Dfn, a.Codecs, a.Bandwidth)
				util.LogColorNoTime("%s", line)
			}
			fmt.Print("请选择最想要的音频流(输入序号): ")
			fmt.Scanf("%d", &aIndex)
			if aIndex < 0 || aIndex >= len(result.AudioTracks) {
				aIndex = 0
			}
		}

		if len(result.VideoTracks) > vIndex && !w.Cfg.AudioOnly {
			selectedVideo = &result.VideoTracks[vIndex]
		}
		if len(result.AudioTracks) > aIndex && !w.Cfg.VideoOnly {
			selectedAudio = &result.AudioTracks[aIndex]
		}

		// PCDN / host replace
		if w.Cfg.ForceReplaceHost && w.Cfg.UposHost == "" {
			w.Cfg.UposHost = backupHost
		}
		handlePcdn(&w.Cfg, selectedVideo, selectedAudio)

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

		// Cover
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

		// Video
		videoPath := ""
		if selectedVideo != nil {
			videoPath = filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.%s.mp4", page.Aid, page.Index, page.Cid))
			os.MkdirAll(page.Aid, 0755)
			util.Log("开始下载P%d视频...", page.Index)
			if err := download.DownloadFile(ctx, selectedVideo.BaseURL, videoPath, dlCfg); err != nil {
				if retry++; retry >= maxRetries {
					util.LogError("P%d 视频下载失败: %v", page.Index, err)
					return false
				}
				util.LogWarn("下载异常, 3秒后重试...")
				time.Sleep(3 * time.Second)
				continue
			}
		}

		// Audio
		audioPath := ""
		if selectedAudio != nil {
			audioPath = filepath.Join(page.Aid, fmt.Sprintf("%s.P%d.%s.m4a", page.Aid, page.Index, page.Cid))
			os.MkdirAll(page.Aid, 0755)
			util.Log("开始下载P%d音频...", page.Index)
			if err := download.DownloadFile(ctx, selectedAudio.BaseURL, audioPath, dlCfg); err != nil {
				if retry++; retry >= maxRetries {
					util.LogError("P%d 音频下载失败: %v", page.Index, err)
					return false
				}
				time.Sleep(3 * time.Second)
				continue
			}
		}

		// Mux or save directly
		if !w.Cfg.SkipMux && videoPath != "" && audioPath != "" {
			util.Log("开始合并音视频...")
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
				nil, w.Cfg.AudioOnly, w.Cfg.VideoOnly, w.Cfg.SimplyMux,
				nil, pubTime, isHevc); err != nil {
				util.LogError("合并失败: %v", err)
				return false
			}
		} else if w.Cfg.AudioOnly && audioPath != "" {
			os.Rename(audioPath, savePath)
		}

		// Cleanup temp files
		util.Log("清理临时文件...")
		time.Sleep(200 * time.Millisecond)
		if videoPath != "" {
			os.Remove(videoPath)
		}
		if audioPath != "" {
			os.Remove(audioPath)
		}
		coverPath := filepath.Join(page.Aid, page.Aid+".jpg")
		if pagesCount == 1 || page.Index == allPages[len(allPages)-1].Index || page.Aid != allPages[len(allPages)-1].Aid {
			os.Remove(coverPath)
		}
		if dir, _ := os.ReadDir(page.Aid); len(dir) == 0 {
			os.Remove(page.Aid)
		}

		util.Log("下载P%d完毕", page.Index)

		return true
	}
	return false
}

func (w *Workflow) loadCredentials() {
	appDir := appDirFunc()
	if w.Cfg.Cookie == "" {
		data, err := os.ReadFile(filepath.Join(appDir, "BBDown.data"))
		if err == nil {
			w.Cfg.Cookie = strings.TrimSpace(string(data))
		}
	}
	if w.Cfg.AccessToken == "" && w.Cfg.UseTvAPI {
		data, err := os.ReadFile(filepath.Join(appDir, "BBDownTV.data"))
		if err == nil {
			w.Cfg.AccessToken = strings.TrimPrefix(strings.TrimSpace(string(data)), "access_token=")
		}
	}
	if w.Cfg.AccessToken == "" && w.Cfg.UseAppAPI {
		data, err := os.ReadFile(filepath.Join(appDir, "BBDownApp.data"))
		if err == nil {
			w.Cfg.AccessToken = strings.TrimPrefix(strings.TrimSpace(string(data)), "access_token=")
		}
	}
}

func (w *Workflow) notifyCompletion(vInfo *entity.VInfo, success bool) {
	msg := "completed"
	if !success {
		msg = "completed-with-failures"
	}
	body := fmt.Sprintf(`{"title":"%s","page_count":%d,"message":"%s","completed_at":%d}`,
		vInfo.Title, len(vInfo.PagesInfo), msg, time.Now().Unix())
	util.LogDebug("通知回调: %s", w.Cfg.NotifyWebhook)
	w.HTTPClient.PostResponse(context.Background(), w.Cfg.NotifyWebhook, []byte(body), map[string]string{
		"Content-Type": "application/json",
	})
}

func handlePcdn(cfg *config.MyOption, video *entity.Video, audio *entity.Audio) {
	if cfg.AllowPcdn {
		return
	}
	if cfg.UposHost != "" {
		if video != nil && video.BaseURL != "" {
			video.BaseURL = replaceHost(video.BaseURL, cfg.UposHost)
		}
		if audio != nil && audio.BaseURL != "" {
			audio.BaseURL = replaceHost(audio.BaseURL, cfg.UposHost)
		}
	}
}

func replaceHost(urlStr, newHost string) string {
	if idx := strings.Index(urlStr, "://"); idx >= 0 {
		rest := urlStr[idx+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return urlStr[:idx+3] + newHost + rest[slash:]
		}
	}
	return urlStr
}

func appDirFunc() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func parseEncodingPriority(s string) (map[string]int, string) {
	result := make(map[string]int)
	if s == "" {
		return result, ""
	}
	parts := strings.Split(s, ",")
	for i, p := range parts {
		p = strings.TrimSpace(strings.ReplaceAll(p, "-", ""))
		if p != "" {
			result[strings.ToUpper(p)] = len(parts) - i
		}
	}
	if len(parts) > 0 {
		return result, strings.TrimSpace(parts[0])
	}
	return result, ""
}

func parseDfnPriority(s string) map[string]int {
	result := make(map[string]int)
	if s == "" {
		return result
	}
	parts := strings.Split(s, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result[p] = len(parts) - i
		}
	}
	return result
}

func parseDanmakuFormats(s string) []string {
	if s == "" {
		return []string{"xml", "ass"}
	}
	return strings.Split(s, ",")
}
