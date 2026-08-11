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
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// DownloadConfig holds download options.
type DownloadConfig struct {
	UseAria2c  bool
	Aria2cArgs string
	ForceHTTP  bool
	MultiThread bool
}

// DownloadFile downloads a URL to a local file.
func DownloadFile(ctx context.Context, url, destPath string, cfg DownloadConfig) error {
	_ = ctx
	if cfg.UseAria2c {
		return downloadWithAria2c(url, destPath, cfg.Aria2cArgs)
	}
	return downloadWithHTTP(url, destPath)
}

func downloadWithHTTP(url, destPath string) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Force HTTP if configured
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

func downloadWithAria2c(url, destPath, aria2cArgs string) error {
	args := []string{"-x", "16", "-s", "16", "-o", filepath.Base(destPath), "-d", filepath.Dir(destPath)}
	if aria2cArgs != "" {
		args = append(args, strings.Fields(aria2cArgs)...)
	}
	args = append(args, url)

	cmd := exec.Command("aria2c", args...)
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

// PrintAllTracks displays available video and audio tracks.
func PrintAllTracks(result *entity.ParsedResult, declaredDur int, onlyShowInfo bool) {
	util.LogColor("%s", "视频流:")
	for i, v := range result.VideoTracks {
		kbps := float64(0)
		if v.Dur > 0 {
			kbps = v.Size / 1024 / float64(v.Dur) * 8
		}
			util.LogColor("%s", fmt.Sprintf("  %d. [%s] [%s] [%s] [%s] [~%02.0f kbps] [%s]",
				i, v.Dfn, v.Res, v.Codecs, v.FPS, kbps, util.FormatFileSize(v.Size)))
	}
	util.LogColor("%s", "音频流:")
	for i, a := range result.AudioTracks {
		util.LogColor("%s", fmt.Sprintf("  %d. [%s] [%s] [~%d kbps]", i, a.Dfn, a.Codecs, a.Bandwidth))
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
