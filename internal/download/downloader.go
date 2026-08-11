package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// DownloadConfig holds download options.
type DownloadConfig struct {
	UseAria2c   bool
	Aria2cArgs  string
	Aria2cPath  string
	ForceHTTP   bool
	MultiThread bool
}

// ThreadSegmentSize returns the segment size in MB.
func (c DownloadConfig) ThreadSegmentSize() int { return 20 }

// DownloadFile downloads a URL to a local file, with optional multi-threading.
func DownloadFile(ctx context.Context, url, destPath string, cfg DownloadConfig) error {
	_ = ctx
	if cfg.UseAria2c {
		return downloadWithAria2c(url, destPath, cfg)
	}
	if cfg.MultiThread && !strings.Contains(url, "-cmcc-") {
		size, _ := getFileSize(url)
		if size > int64(cfg.ThreadSegmentSize())*1024*1024 {
			return multiThreadDownload(url, destPath, cfg)
		}
	} else if cfg.MultiThread && strings.Contains(url, "-cmcc-") {
		util.LogWarn("检测到cmcc域名cdn, 已经禁用多线程")
	}
	return singleDownload(url, destPath)
}

func singleDownload(url, destPath string) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if strings.HasPrefix(url, "https://") {
		url = strings.Replace(url, "https://", "http://", 1)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func getFileSize(url string) (int64, error) {
	if strings.HasPrefix(url, "https://") {
		url = strings.Replace(url, "https://", "http://", 1)
	}
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD failed: HTTP %d", resp.StatusCode)
	}
	return resp.ContentLength, nil
}

func multiThreadDownload(url, destPath string, cfg DownloadConfig) error {
	dir := filepath.Dir(destPath)
	os.MkdirAll(dir, 0755)

	if strings.HasPrefix(url, "https://") {
		url = strings.Replace(url, "https://", "http://", 1)
	}

	size, err := getFileSize(url)
	if err != nil || size <= 0 {
		return singleDownload(url, destPath)
	}

	// Check if destination already exists with correct size
	if info, err := os.Stat(destPath); err == nil && info.Size() == size {
		return nil
	}

	segSize := int64(cfg.ThreadSegmentSize()) * 1024 * 1024
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

	// Parallel download
	var totalBytes int64
	var wg sync.WaitGroup
	errCh := make(chan error, len(clips))

	for _, clip := range clips {
		wg.Add(1)
		go func(c clipRange) {
			defer wg.Done()
			n, err := downloadRange(url, destPath, c)
			if err != nil {
				errCh <- err
				return
			}
			atomic.AddInt64(&totalBytes, n)
		}(clip)
	}
	wg.Wait()
	close(errCh)

	// If any segment failed, fall back to single-threaded
	if len(errCh) > 0 {
		// Clean up clip files
		for _, c := range clips {
			os.Remove(clipPath(destPath, c.idx))
		}
		return singleDownload(url, destPath)
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
	idx       int
	from, to  int64
}

func clipPath(dest string, idx int) string {
	ext := filepath.Ext(dest) + ".clip"
	return strings.TrimSuffix(dest, filepath.Ext(dest)) + "." + strconv.Itoa(idx) + ext
}

func downloadRange(url, destPath string, clip clipRange) (int64, error) {
	tmpPath := clipPath(destPath, clip.idx)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", clip.from, clip.to))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("range request failed: HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	return io.Copy(out, resp.Body)
}

// ThreadSegmentSize returns the segment size in MB, with a minimum of 1.

func downloadWithAria2c(url, destPath string, cfg DownloadConfig) error {
	bin := "aria2c"
	if cfg.Aria2cPath != "" {
		bin = cfg.Aria2cPath
	}
	args := []string{"-x", "16", "-s", "16", "-o", filepath.Base(destPath), "-d", filepath.Dir(destPath)}
	if cfg.Aria2cArgs != "" {
		args = append(args, strings.Fields(cfg.Aria2cArgs)...)
	}
	args = append(args, url)

	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// SortVideoTracks sorts video tracks by encoding and quality priority.
func SortVideoTracks(tracks []entity.Video, dfnPriority, encodingPriority map[string]int, ascending bool) []entity.Video {
	sorted := make([]entity.Video, len(tracks))
	copy(sorted, tracks)
	sort.SliceStable(sorted, func(i, j int) bool {
		// Sort by encoding priority first
		epI := encodingPriority[sorted[i].Codecs]
		epJ := encodingPriority[sorted[j].Codecs]
		if epI != epJ {
			return epI > epJ
		}
		// Then by quality priority
		dpI := dfnPriority[sorted[i].Dfn]
		dpJ := dfnPriority[sorted[j].Dfn]
		if dpI != dpJ {
			return dpI > dpJ
		}
		// Then by bandwidth
		if ascending {
			return sorted[i].Bandwidth < sorted[j].Bandwidth
		}
		return sorted[i].Bandwidth > sorted[j].Bandwidth
	})
	return sorted
}

// SortAudioTracks sorts audio tracks by encoding priority.
func SortAudioTracks(tracks []entity.Audio, encodingPriority map[string]int, ascending bool) []entity.Audio {
	sorted := make([]entity.Audio, len(tracks))
	copy(sorted, tracks)
	sort.SliceStable(sorted, func(i, j int) bool {
		epI := encodingPriority[sorted[i].Codecs]
		epJ := encodingPriority[sorted[j].Codecs]
		if epI != epJ {
			return epI > epJ
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
		fmt.Println()
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
		fmt.Println()
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

// ParsePriority converts a comma-separated priority string to a map with index values.
func ParsePriority(priorityStr string) map[string]int {
	if priorityStr == "" {
		return nil
	}
	result := make(map[string]int)
	parts := strings.Split(priorityStr, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result[p] = len(parts) - i // higher index = higher priority
		}
	}
	return result
}

// FormatSavePath builds the output file path from the save pattern.
func FormatSavePath(pattern, title string, video *entity.Video, audio *entity.Audio, page entity.Page, pagesCount int, apiType string, pubTime int64) string {
	result := pattern
	result = strings.ReplaceAll(result, "<videoTitle>", util.GetValidFileName(title, "_", true))
	result = strings.ReplaceAll(result, "<pageNumber>", fmt.Sprintf("%d", page.Index))
	result = strings.ReplaceAll(result, "<pageNumberWithZero>", fmt.Sprintf("%02d", page.Index))
	result = strings.ReplaceAll(result, "<pageTitle>", util.GetValidFileName(page.Title, "_", true))
	result = strings.ReplaceAll(result, "<aid>", page.Aid)
	result = strings.ReplaceAll(result, "<cid>", page.Cid)
	result = strings.ReplaceAll(result, "<bvid>", page.Bvid())
	result = strings.ReplaceAll(result, "<apiType>", apiType)

	if video != nil {
		result = strings.ReplaceAll(result, "<dfn>", video.Dfn)
		result = strings.ReplaceAll(result, "<codecs>", video.Codecs)
		result = strings.ReplaceAll(result, "<res>", video.Res)
		result = strings.ReplaceAll(result, "<fps>", video.FPS)
	}

	if pubTime > 0 {
		t := time.Unix(pubTime, 0)
		result = strings.ReplaceAll(result, "<yyyy>", t.Format("2006"))
		result = strings.ReplaceAll(result, "<MM>", t.Format("01"))
		result = strings.ReplaceAll(result, "<dd>", t.Format("02"))
	}

	return result
}

// Title ... dumy reference to avoid unused import
var _ = config.DefaultMyOption
