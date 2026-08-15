package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// DownloadConfig holds download options (mirrors upstream MyOption semantics).
type DownloadConfig struct {
	UseAria2c     bool
	Aria2cArgs    string
	Aria2cPath    string
	ForceHTTP     bool
	MultiThread   bool
	SegmentSizeMB int
	RetryCount    int
	RetryDelayMs  int
	Cookie        string
	Client        *util.HTTPClient
}

// maxConcurrentClips caps parallel range requests per file (upstream uses
// MaxDegreeOfParallelism = min(8, max(1, CPU count))).
func maxConcurrentClips() int {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

func (c DownloadConfig) segmentSize() int {
	if c.SegmentSizeMB < 1 {
		return 20
	}
	return c.SegmentSizeMB
}

func (c DownloadConfig) retryCount() int {
	if c.RetryCount < 1 {
		return 3
	}
	return c.RetryCount
}

func (c DownloadConfig) retryDelay() time.Duration {
	if c.RetryDelayMs < 0 {
		return 3 * time.Second
	}
	return time.Duration(c.RetryDelayMs) * time.Millisecond
}

// ErrRangeNotSupported signals that the server ignored a Range request header;
// the caller falls back to single-threaded download (upstream NotSupportedException).
var ErrRangeNotSupported = fmt.Errorf("服务器不支持 Range 请求")

// probeResult is the outcome of a HEAD probe of a download URL.
type probeResult struct {
	size         int64
	ranges       bool
	etag         string
	lastModified string
}

// resumeManifest identifies a remote resource so a partial .tmp file can be
// resumed safely (an untrusted/stale prefix is discarded instead of producing
// a corrupted file). Stored next to the .tmp file.
type resumeManifest struct {
	URL          string `json:"url"`
	Size         int64  `json:"size"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// DownloadFile downloads a URL to a local file, with optional multi-threading,
// resume support and retries (mirrors upstream BBDownDownloadUtil).
func DownloadFile(ctx context.Context, url, destPath string, cfg DownloadConfig) error {
	if cfg.UseAria2c {
		return downloadWithAria2c(ctx, url, destPath, cfg)
	}

	// Force-http replacement: gated by the option, mcdn excluded (upstream).
	url = forceHTTPIfNeeded(url, cfg.ForceHTTP)

	multi := cfg.MultiThread
	if multi && strings.Contains(url, "-cmcc-") {
		util.LogWarn("检测到cmcc域名cdn, 已经禁用多线程")
		multi = false
	}

	pr, err := probeFile(ctx, url, cfg)
	if err == nil && pr.size > 0 {
		if info, statErr := os.Stat(destPath); statErr == nil && info.Size() == pr.size {
			util.Log("%s 已存在, 跳过下载...", destPath)
			return nil
		}
	}

	if multi && pr.ranges && pr.size > int64(cfg.segmentSize())*1024*1024 {
		err := multiThreadDownload(ctx, url, destPath, pr.size, cfg)
		if err == ErrRangeNotSupported {
			util.LogWarn("服务器可能并不支持多线程下载, 请使用 --multi-thread false")
			return singleDownload(ctx, url, destPath, pr, cfg)
		}
		return err
	}
	return singleDownload(ctx, url, destPath, pr, cfg)
}

// forceHTTPIfNeeded downgrades https to http only when the option is enabled
// (upstream: default off; .mcdn.bilivideo.cn never downgraded).
func forceHTTPIfNeeded(url string, force bool) string {
	if !force {
		return url
	}
	if strings.Contains(url, ".mcdn.bilivideo.cn:") {
		return url
	}
	return strings.Replace(url, "https:", "http:", 1)
}

// needsBilibiliReferer mirrors upstream BBDownDownloadUtil: app/TV 平台
// (platform=android*) 的下载 URL 不能携带 Referer，否则 CDN 返回 403；
// web (pc) URL 则必须携带。
func needsBilibiliReferer(url string) bool {
	return !strings.Contains(url, "platform=android_tv_yst") && !strings.Contains(url, "platform=android")
}

// probeFile issues a HEAD request and returns content length, whether the
// server advertises byte-range support, and validator headers for resume.
func probeFile(ctx context.Context, url string, cfg DownloadConfig) (probeResult, error) {
	pr := probeResult{}
	client := cfg.Client.DownloadClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return pr, err
	}
	if needsBilibiliReferer(url) {
		req.Header.Set("Referer", "https://www.bilibili.com")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return pr, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return pr, fmt.Errorf("HEAD failed: HTTP %d", resp.StatusCode)
	}
	pr.size = resp.ContentLength
	pr.ranges = strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes")
	pr.etag = resp.Header.Get("ETag")
	pr.lastModified = resp.Header.Get("Last-Modified")
	return pr, nil
}

// singleDownload downloads with resume support: partial data goes to dest+".tmp"
// and is renamed on success. An existing .tmp is resumed via Range only when a
// sidecar manifest proves the remote resource identity matches, otherwise the
// stale prefix is discarded (upstream resume-manifest semantics).
func singleDownload(ctx context.Context, url, destPath string, pr probeResult, cfg DownloadConfig) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := destPath + ".tmp"
	metaPath := tmp + ".meta"

	readManifest := func() (resumeManifest, bool) {
		var m resumeManifest
		data, err := os.ReadFile(metaPath)
		if err != nil || json.Unmarshal(data, &m) != nil {
			return m, false
		}
		return m, true
	}

	// identityMatches reports whether the recorded resource identity matches the
	// current probe: the URL must match, and any validator the server provides
	// must equal the recorded one.
	identityMatches := func(m resumeManifest) bool {
		if m.URL != url {
			return false
		}
		if pr.etag != "" && m.ETag != pr.etag {
			return false
		}
		if pr.lastModified != "" && m.LastModified != pr.lastModified {
			return false
		}
		return true
	}

	// Write the manifest BEFORE the first byte: an interrupted .tmp must carry a
	// manifest or the next run cannot trust it and has to redownload.
	if pr.size > 0 {
		m := resumeManifest{URL: url, Size: pr.size, ETag: pr.etag, LastModified: pr.lastModified}
		if data, err := json.Marshal(m); err == nil {
			os.WriteFile(metaPath, data, 0o644)
		}
	}

	lastErr := fmt.Errorf("download failed")
	for attempt := 0; attempt < cfg.retryCount(); attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * cfg.retryDelay()
			util.LogWarn("下载异常, %v 后重试... (%d/%d)", backoff, attempt, cfg.retryCount())
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
		}

		var offset int64
		var ifRange string
		if pr.size > 0 {
			if info, err := os.Stat(tmp); err == nil {
				m, ok := readManifest()
				trusted := ok && identityMatches(m)
				switch {
				case info.Size() == pr.size && trusted:
					// Complete temp file with matching identity: adopt it.
					util.LogDebug("断点续传: 检测到已完整下载的临时文件且资源身份一致, 直接移动")
					if err := os.Rename(tmp, destPath); err != nil {
						return err
					}
					os.Remove(metaPath)
					return nil
				case info.Size() > 0 && info.Size() < pr.size && trusted:
					offset = info.Size()
					if m.ETag != "" {
						ifRange = m.ETag
					} else if m.LastModified != "" {
						ifRange = m.LastModified
					}
					util.LogDebug("断点续传: 从现有临时文件 %d 字节处继续（资源身份一致）", offset)
				case !trusted || info.Size() > pr.size:
					// Stale prefix / changed resource / oversized tmp: discard.
					util.LogDebug("断点续传: 临时文件资源身份不可信, 删除后完整重下")
					os.Remove(tmp)
					os.Remove(metaPath)
				}
			}
		}

		err := func() error {
			client := cfg.Client.DownloadClient()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			if needsBilibiliReferer(url) {
				req.Header.Set("Referer", "https://www.bilibili.com")
			}
			req.Header.Set("User-Agent", "Mozilla/5.0")
			if cfg.Cookie != "" {
				req.Header.Set("Cookie", cfg.Cookie)
			}
			if offset > 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
				if ifRange != "" {
					// Let the server confirm the local prefix still belongs to this
					// resource; a 200/412 response makes us restart/rewrite below.
					req.Header.Set("If-Range", ifRange)
				}
			}

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			switch {
			case resp.StatusCode == http.StatusPartialContent:
				if offset > 0 {
					if got := rangeStart(resp.Header.Get("Content-Range")); got >= 0 && got != offset {
						// Server ignored our offset: restart from scratch.
						offset = 0
						if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
							return err
						}
					}
				}
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				// Full body (no range support or If-Range mismatch): restart.
				offset = 0
				if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
					return err
				}
			default:
				return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
			}

			out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer out.Close()
			if _, err := out.Seek(offset, io.SeekStart); err != nil {
				return err
			}

			if isTerminalOut() && resp.ContentLength > 0 {
				pr2 := newProgressReader(resp.Body, resp.ContentLength+offset)
				defer pr2.Close()
				_, err = io.Copy(out, pr2)
			} else {
				_, err = io.Copy(out, resp.Body)
			}
			return err
		}()
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		// Verify the final length before adopting the file.
		if pr.size > 0 {
			if info, statErr := os.Stat(tmp); statErr != nil || info.Size() != pr.size {
				lastErr = fmt.Errorf("下载产物长度(%d)与服务器声明(%d)不符", fileSizeOrZero(tmp), pr.size)
				continue
			}
		}

		if err := os.Rename(tmp, destPath); err != nil {
			return err
		}
		os.Remove(metaPath)
		return nil
	}
	return lastErr
}

// fileSizeOrZero returns the size of a file, or 0 on error.
func fileSizeOrZero(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// rangeStart parses the start offset from a Content-Range header ("bytes N-M/S").
func rangeStart(contentRange string) int64 {
	if contentRange == "" {
		return -1
	}
	rest := strings.TrimPrefix(strings.ToLower(contentRange), "bytes ")
	if idx := strings.Index(rest, "-"); idx > 0 {
		if n, err := strconv.ParseInt(rest[:idx], 10, 64); err == nil {
			return n
		}
	}
	return -1
}

// multiThreadDownload downloads segments concurrently, validates range responses
// and falls back to single-threaded download when the server ignores Range.
func multiThreadDownload(ctx context.Context, url, destPath string, size int64, cfg DownloadConfig) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	segSize := int64(cfg.segmentSize()) * 1024 * 1024
	var clips []clipRange
	var offset int64
	idx := 0
	for offset < size {
		end := offset + segSize - 1
		if end >= size {
			end = size - 1
		}
		clips = append(clips, clipRange{idx: idx, from: offset, to: end})
		offset = end + 1
		idx++
	}

	var totalBytes atomic.Int64
	progressDone := make(chan struct{})
	go renderAggregateProgress(&totalBytes, size, progressDone)

	sem := make(chan struct{}, maxConcurrentClips())
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var notSupported atomic.Bool

	recordErr := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	for _, clip := range clips {
		if notSupported.Load() {
			break
		}
		wg.Add(1)
		go func(c clipRange) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				recordErr(ctx.Err())
				return
			}

			n, err := downloadRange(ctx, url, destPath, c, cfg)
			if err == ErrRangeNotSupported {
				notSupported.Store(true)
				recordErr(err)
				return
			}
			if err != nil {
				recordErr(err)
				return
			}
			totalBytes.Add(n)
		}(clip)
	}
	wg.Wait()
	close(progressDone)

	if notSupported.Load() || firstErr != nil {
		for _, c := range clips {
			os.Remove(clipPath(destPath, c.idx))
		}
		if notSupported.Load() {
			return ErrRangeNotSupported
		}
		return firstErr
	}

	// Verify every clip has the expected byte count before merging.
	for _, c := range clips {
		info, err := os.Stat(clipPath(destPath, c.idx))
		if err != nil || info.Size() != c.to-c.from+1 {
			for _, cc := range clips {
				os.Remove(clipPath(destPath, cc.idx))
			}
			return fmt.Errorf("分片大小校验失败")
		}
	}

	// Merge clip files
	util.Log("合并分片...")
	var clipFiles []string
	for _, c := range clips {
		clipFiles = append(clipFiles, clipPath(destPath, c.idx))
	}
	if err := util.CombineMultipleFilesIntoSingleFile(clipFiles, destPath); err != nil {
		return err
	}
	util.Log("清理分片...")
	for _, f := range clipFiles {
		os.Remove(f)
	}
	return nil
}

type clipRange struct {
	idx      int
	from, to int64
}

func clipPath(dest string, idx int) string {
	return fmt.Sprintf("%s.%03d.vclip", dest, idx)
}

// downloadRange downloads one byte range with per-clip retries and validates
// that the server honored the requested offset.
func downloadRange(ctx context.Context, url, destPath string, clip clipRange, cfg DownloadConfig) (int64, error) {
	tmpPath := clipPath(destPath, clip.idx)

	var lastErr error
	for attempt := 0; attempt < cfg.retryCount(); attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * cfg.retryDelay()
			if !sleepCtx(ctx, backoff) {
				return 0, ctx.Err()
			}
		}

		n, err := func() (int64, error) {
			client := cfg.Client.DownloadClient()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return 0, err
			}
			if needsBilibiliReferer(url) {
				req.Header.Set("Referer", "https://www.bilibili.com")
			}
			req.Header.Set("User-Agent", "Mozilla/5.0")
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", clip.from, clip.to))
			if cfg.Cookie != "" {
				req.Header.Set("Cookie", cfg.Cookie)
			}

			resp, err := client.Do(req)
			if err != nil {
				return 0, err
			}
			defer resp.Body.Close()

			switch {
			case resp.StatusCode == http.StatusPartialContent:
				if got := rangeStart(resp.Header.Get("Content-Range")); got >= 0 && got != clip.from {
					return 0, fmt.Errorf("range request ignored: expected offset %d, got %d", clip.from, got)
				}
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				// Server sent the full body: it does not support ranges.
				return 0, ErrRangeNotSupported
			default:
				return 0, fmt.Errorf("range request failed: HTTP %d", resp.StatusCode)
			}

			out, err := os.Create(tmpPath)
			if err != nil {
				return 0, err
			}
			defer out.Close()
			return io.Copy(out, resp.Body)
		}()
		if err == nil {
			return n, nil
		}
		if err == ErrRangeNotSupported || ctx.Err() != nil {
			return 0, err
		}
		lastErr = err
		os.Remove(tmpPath)
	}
	return 0, lastErr
}

// renderAggregateProgress draws a 40-block progress bar for multi-thread downloads.
func renderAggregateProgress(counter *atomic.Int64, total int64, done chan struct{}) {
	if !isTerminalOut() {
		return
	}
	ticker := time.NewTicker(125 * time.Millisecond)
	defer ticker.Stop()
	chars := "|/-\\"
	animIdx := 0
	lastBytes := int64(0)
	lastTime := time.Now()
	speed := ""
	render := func() {
		cur := counter.Load()
		now := time.Now()
		if elapsed := now.Sub(lastTime).Seconds(); elapsed >= 1.0 {
			delta := cur - lastBytes
			if delta > 0 {
				speed = " " + formatSpeed(float64(delta)) + "/s"
			}
			lastBytes = cur
			lastTime = now
		}
		pct := float64(cur) / float64(total)
		if pct > 1 {
			pct = 1
		}
		blocks := int(pct * 40)
		bar := strings.Repeat("#", blocks) + strings.Repeat("-", 40-blocks)
		fmt.Printf("                            [%s] %6.2f%% %c%s\r", bar, pct*100, chars[animIdx%len(chars)], speed)
		animIdx++
	}

	// 立即绘制首帧；结束时补一帧最终状态(100%)并换行（与单线程进度条一致）。
	render()
	for {
		select {
		case <-done:
			render()
			fmt.Print("\n")
			return
		case <-ticker.C:
			render()
		}
	}
}

func isTerminalOut() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// downloadWithAria2c invokes aria2c with the upstream-compatible argument set,
// passing URL/headers/cookie/dir/out via stdin (input-file) so credentials do
// not appear on the command line.
func downloadWithAria2c(ctx context.Context, url, destPath string, cfg DownloadConfig) error {
	bin := "aria2c"
	if cfg.Aria2cPath != "" {
		bin = cfg.Aria2cPath
	}
	args := []string{
		"--auto-file-renaming=false",
		"--download-result=hide",
		"--allow-overwrite=true",
		"--console-log-level=warn",
		"-x16", "-s16", "-j16", "-k5M",
	}
	if cfg.Aria2cArgs != "" {
		args = append(args, strings.Fields(cfg.Aria2cArgs)...)
	}
	args = append(args, "--input-file=-")

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer stdin.Close()
		var sb strings.Builder
		sb.WriteString(url + "\n")
		if needsBilibiliReferer(url) {
			sb.WriteString("  header=Referer: https://www.bilibili.com\n")
		}
		sb.WriteString("  header=User-Agent: Mozilla/5.0\n")
		if cfg.Cookie != "" {
			// aria2c input-file lines cannot contain newlines: sanitize so a
			// malicious --cookie value cannot inject extra options.
			cookie := strings.NewReplacer("\r", "", "\n", "").Replace(cfg.Cookie)
			sb.WriteString("  header=Cookie: " + cookie + "\n")
		}
		sb.WriteString("  dir=" + filepath.Dir(destPath) + "\n")
		sb.WriteString("  out=" + filepath.Base(destPath) + "\n")
		io.WriteString(stdin, sb.String())
	}()

	if err := cmd.Run(); err != nil {
		return err
	}
	if _, err := os.Stat(destPath + ".aria2"); err == nil {
		return fmt.Errorf("aria2下载可能存在错误")
	}
	if _, err := os.Stat(destPath); err != nil {
		return fmt.Errorf("aria2下载可能存在错误: 未找到输出文件")
	}
	return nil
}

// SortVideoTracks sorts video tracks by encoding and quality priority
// (upstream TrackSort: codecs priority, dfn priority, id descending, bandwidth).
func SortVideoTracks(tracks []entity.Video, dfnPriority, encodingPriority map[string]int, ascending bool) []entity.Video {
	sorted := make([]entity.Video, len(tracks))
	copy(sorted, tracks)
	sort.SliceStable(sorted, func(i, j int) bool {
		epI, ok := encodingPriority[sorted[i].Codecs]
		if !ok {
			epI = 100
		}
		epJ, ok := encodingPriority[sorted[j].Codecs]
		if !ok {
			epJ = 100
		}
		if epI != epJ {
			return epI < epJ
		}
		dpI, ok := dfnPriority[sorted[i].Dfn]
		if !ok {
			dpI = 100
		}
		dpJ, ok := dfnPriority[sorted[j].Dfn]
		if !ok {
			dpJ = 100
		}
		if dpI != dpJ {
			return dpI < dpJ
		}
		idI, _ := strconv.Atoi(sorted[i].ID)
		idJ, _ := strconv.Atoi(sorted[j].ID)
		if idI != idJ {
			return idI > idJ
		}
		if ascending {
			return sorted[i].Bandwidth < sorted[j].Bandwidth
		}
		return sorted[i].Bandwidth > sorted[j].Bandwidth
	})
	return sorted
}

// SortAudioTracks sorts audio tracks by encoding priority (upstream uses the
// short codec name, e.g. E-AC-3 => EAC3).
func SortAudioTracks(tracks []entity.Audio, encodingPriority map[string]int, ascending bool) []entity.Audio {
	sorted := make([]entity.Audio, len(tracks))
	copy(sorted, tracks)
	sort.SliceStable(sorted, func(i, j int) bool {
		epI, ok := encodingPriority[sorted[i].ShortCodecs()]
		if !ok {
			epI = 100
		}
		epJ, ok := encodingPriority[sorted[j].ShortCodecs()]
		if !ok {
			epJ = 100
		}
		if epI != epJ {
			return epI < epJ
		}
		if ascending {
			return sorted[i].Bandwidth < sorted[j].Bandwidth
		}
		return sorted[i].Bandwidth > sorted[j].Bandwidth
	})
	return sorted
}

// PrintAllTracks displays available video and audio tracks (matching C# format).
func PrintAllTracks(result *entity.ParsedResult, pageDur int, onlyShowInfo bool) {
	if len(result.VideoTracks) > 0 {
		util.Log("共计%d条视频流.", len(result.VideoTracks))
		for i, v := range result.VideoTracks {
			pDur := pageDur
			if pDur == 0 {
				pDur = v.Dur
			}
			size := v.Size
			if size <= 0 {
				size = float64(pDur) * float64(v.Bandwidth) * 1024 / 8
			}
			line := fmt.Sprintf("%d. [%s] [%s] [%s] [%s] [%d kbps] [~%s]",
				i, v.Dfn, v.Res, v.Codecs, v.FPS, v.Bandwidth, util.FormatFileSize(size))
			line = strings.ReplaceAll(line, "[] ", "")
			util.LogColorNoTime("%s", line)
		}
	}
	if len(result.AudioTracks) > 0 {
		util.Log("共计%d条音频流.", len(result.AudioTracks))
		for i, a := range result.AudioTracks {
			pDur := pageDur
			if pDur == 0 {
				pDur = a.Dur
			}
			line := fmt.Sprintf("%d. [%s] [%d kbps] [~%s]",
				i, a.Codecs, a.Bandwidth, util.FormatFileSize(float64(pDur)*float64(a.Bandwidth)*1024/8))
			util.LogColorNoTime("%s", line)
		}
	}
}

// PrintSelectedTrack shows the chosen tracks (matching C# format).
func PrintSelectedTrack(video *entity.Video, audio *entity.Audio, pageDur int) {
	if video != nil {
		pDur := pageDur
		if pDur == 0 {
			pDur = video.Dur
		}
		size := video.Size
		if size <= 0 {
			size = float64(pDur) * float64(video.Bandwidth) * 1024 / 8
		}
		line := fmt.Sprintf("[视频] [%s] [%s] [%s] [%s] [%d kbps] [~%s]",
			video.Dfn, video.Res, video.Codecs, video.FPS, video.Bandwidth, util.FormatFileSize(size))
		line = strings.ReplaceAll(line, "[] ", "")
		util.LogColorNoTime("%s", line)
	}
	if audio != nil {
		pDur := pageDur
		if pDur == 0 {
			pDur = audio.Dur
		}
		line := fmt.Sprintf("[音频] [%s] [%d kbps] [~%s]",
			audio.Codecs, audio.Bandwidth, util.FormatFileSize(float64(pDur)*float64(audio.Bandwidth)*1024/8))
		util.LogColorNoTime("%s", line)
	}
}

// ParsePriority converts a comma-separated priority string to a map with index
// values (kept for API compatibility; the workflow uses parseEncodingPriority/
// parseDfnPriority with upstream index++ semantics).
func ParsePriority(priorityStr string) map[string]int {
	if priorityStr == "" {
		return nil
	}
	result := make(map[string]int)
	parts := strings.Split(priorityStr, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result[p] = i
		}
	}
	return result
}

// FormatSavePath builds the output file path from the save pattern (upstream
// PathHelper placeholder set).
func FormatSavePath(pattern, title string, video *entity.Video, audio *entity.Audio, page entity.Page, pagesCount int, apiType string, pubTime int64) string {
	result := strings.ReplaceAll(pattern, "\\", "/")
	title = util.GetValidFileName(title, "_", true)
	title = strings.Trim(strings.TrimRight(title, "."), " ")
	pageTitle := util.GetValidFileName(page.Title, "_", true)
	pageTitle = strings.Trim(strings.TrimRight(pageTitle, "."), " ")
	ownerName := util.GetValidFileName(page.OwnerName, "_", true)
	ownerName = strings.Trim(strings.TrimRight(ownerName, "."), " ")

	result = strings.ReplaceAll(result, "<videoTitle>", title)
	result = strings.ReplaceAll(result, "<pageNumber>", fmt.Sprintf("%d", page.Index))
	result = strings.ReplaceAll(result, "<pageNumberWithZero>", fmt.Sprintf("%0*d", digits(pagesCount), page.Index))
	result = strings.ReplaceAll(result, "<pageTitle>", pageTitle)
	result = strings.ReplaceAll(result, "<aid>", page.Aid)
	result = strings.ReplaceAll(result, "<cid>", page.Cid)
	result = strings.ReplaceAll(result, "<bvid>", page.Bvid())
	result = strings.ReplaceAll(result, "<ownerName>", ownerName)
	result = strings.ReplaceAll(result, "<ownerMid>", page.OwnerMid)
	result = strings.ReplaceAll(result, "<apiType>", apiType)

	if video != nil {
		result = strings.ReplaceAll(result, "<dfn>", video.Dfn)
		result = strings.ReplaceAll(result, "<videoCodecs>", video.Codecs)
		result = strings.ReplaceAll(result, "<res>", video.Res)
		result = strings.ReplaceAll(result, "<fps>", video.FPS)
		result = strings.ReplaceAll(result, "<videoBandwidth>", fmt.Sprintf("%d", video.Bandwidth))
	}
	if audio != nil {
		result = strings.ReplaceAll(result, "<audioCodecs>", audio.Codecs)
		result = strings.ReplaceAll(result, "<audioBandwidth>", fmt.Sprintf("%d", audio.Bandwidth))
	}
	result = replaceDatePlaceholder(result, "<publishDate:", pubTime, "yyyy-MM-dd_HH-mm-ss")
	result = replaceDatePlaceholder(result, "<videoDate:", page.PubTime, "yyyy-MM-dd_HH-mm-ss")
	result = strings.ReplaceAll(result, "<publishDate>", dateOrEmpty(pubTime))
	result = strings.ReplaceAll(result, "<videoDate>", dateOrEmpty(page.PubTime))

	return result
}

// replaceDatePlaceholder handles <key:fmt> placeholders.
func replaceDatePlaceholder(s, prefix string, ts int64, defFmt string) string {
	for {
		idx := strings.Index(s, prefix)
		if idx < 0 {
			return s
		}
		end := strings.IndexByte(s[idx:], '>')
		if end < 0 {
			return s
		}
		format := s[idx+len(prefix) : idx+end]
		if format == "" {
			format = defFmt
		}
		value := ""
		if ts > 0 {
			value = formatTimeStamp(ts, format)
		}
		s = s[:idx] + value + s[idx+end+1:]
	}
}

// dateOrEmpty formats the default timestamp, empty for zero.
func dateOrEmpty(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return formatTimeStamp(ts, "yyyy-MM-dd_HH-mm-ss")
}

// digits returns the number of decimal digits of n (min 1).
func digits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		n /= 10
		d++
	}
	return d
}

// formatTimeStamp converts a unix timestamp with a yyyy-MM-dd_HH-mm-ss style
// format string (upstream FormatTimeStamp token subset).
func formatTimeStamp(ts int64, format string) string {
	t := time.Unix(ts, 0).Local()
	r := strings.NewReplacer(
		"yyyy", "2006",
		"MM", "01",
		"dd", "02",
		"HH", "15",
		"mm", "04",
		"ss", "05",
		"M", "1",
		"d", "2",
		"H", "3",
		"m", "4",
		"s", "5",
	)
	return t.Format(r.Replace(format))
}
