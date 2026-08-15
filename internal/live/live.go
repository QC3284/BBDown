package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/util"
)

const reconnectLimit = 3

// ResolveLive resolves a Bilibili live room ID to a stream URL and metadata.
func ResolveLive(ctx context.Context, roomID string, client *util.HTTPClient) (streamURL, title, uname string, err error) {
	if _, err := strconv.ParseInt(roomID, 10, 64); err != nil {
		return "", "", "", fmt.Errorf("直播间 ID 必须是数字，当前值: %q", roomID)
	}

	// Get room info
	infoAPI := "https://api.live.bilibili.com/room/v1/Room/get_info?room_id=" + roomID
	infoJSON, err := client.GetWebSource(ctx, infoAPI)
	if err != nil {
		return "", "", "", fmt.Errorf("获取直播间信息失败: %w", err)
	}

	var infoResult struct {
		Code int `json:"code"`
		Data struct {
			Title      string `json:"title"`
			Uname      string `json:"uname"`
			LiveStatus int    `json:"live_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(infoJSON), &infoResult); err != nil {
		return "", "", "", fmt.Errorf("解析直播间信息: %w", err)
	}

	d := infoResult.Data
	title = d.Title
	if title == "" {
		title = "直播间" + roomID
	}
	uname = d.Uname
	if d.LiveStatus != 1 {
		return "", "", "", fmt.Errorf("直播间 %s 当前未在直播", roomID)
	}

	// Get stream URL
	playAPI := fmt.Sprintf(
		"https://api.live.bilibili.com/xlive/web-room/v2/index/getRoomPlayInfo?room_id=%s&protocol=0,1&format=0,1,2&codec=0,1&qn=10000&platform=web",
		roomID,
	)
	playJSON, err := client.GetWebSource(ctx, playAPI)
	if err != nil {
		return "", "", "", fmt.Errorf("获取直播流地址失败: %w", err)
	}

	var playResult struct {
		Code int `json:"code"`
		Data struct {
			PlayurlInfo struct {
				Playurl struct {
					Stream []struct {
						Format []struct {
							FormatName string `json:"format_name"`
							Codec      []struct {
								BaseURL string `json:"base_url"`
								URLInfo []struct {
									Host  string `json:"host"`
									Extra string `json:"extra"`
								} `json:"url_info"`
							} `json:"codec"`
						} `json:"format"`
					} `json:"stream"`
				} `json:"playurl"`
			} `json:"playurl_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(playJSON), &playResult); err != nil {
		return "", "", "", fmt.Errorf("解析直播流信息: %w", err)
	}

	for _, stream := range playResult.Data.PlayurlInfo.Playurl.Stream {
		for _, format := range stream.Format {
			if format.FormatName != "flv" {
				continue
			}
			for _, codec := range format.Codec {
				baseURL := codec.BaseURL
				if baseURL == "" {
					continue
				}
				for _, urlInfo := range codec.URLInfo {
					if urlInfo.Host == "" {
						continue
					}
					return urlInfo.Host + baseURL + urlInfo.Extra, title, uname, nil
				}
			}
		}
	}

	return "", "", "", fmt.Errorf("无法获取直播间 %s 的可录制流地址", roomID)
}

// DownloadToFile records a live stream with automatic reconnection, writing
// per-connection segments which are merged with ffmpeg at the end (upstream
// LiveStreamUtil). Cancellation stops recording but still finalizes segments.
func DownloadToFile(ctx context.Context, roomID, path string, client *util.HTTPClient) (bool, error) {
	segRoot := path + ".segs"
	sessionDir := filepath.Join(segRoot, "session-"+time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return false, err
	}
	defer os.RemoveAll(segRoot)

	var segFiles []string
	var total int64
	reconnect := 0
	segIdx := 0

	for {
		streamURL, _, _, err := ResolveLive(ctx, roomID, client)
		if err != nil {
			// Live has ended: finish normally.
			if strings.Contains(err.Error(), "当前未在直播") {
				break
			}
			reconnect++
			if reconnect > reconnectLimit {
				util.LogWarn("直播流中断且 %d 次重连失败，已录制内容保留在 %s", reconnectLimit, segRoot)
				return total > 0, fmt.Errorf("重连失败: %w", err)
			}
			backoff := time.Duration(3000*reconnect) * time.Millisecond
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
			util.LogWarn("直播流中断（%v），%v 后重连（%d/%d）...", err, backoff, reconnect, reconnectLimit)
			if !sleepCtx(ctx, backoff) {
				break
			}
			continue
		}

		segPath := filepath.Join(sessionDir, fmt.Sprintf("seg-%03d.flv", segIdx))
		segIdx++
		n, err := streamToFile(ctx, streamURL, segPath)
		if err != nil {
			os.Remove(segPath)
			if ctx.Err() != nil {
				break
			}
			reconnect++
			if reconnect > reconnectLimit {
				util.LogWarn("直播流中断且 %d 次重连失败，已录制内容保留在 %s", reconnectLimit, segRoot)
				return total > 0, fmt.Errorf("流传输失败: %w", err)
			}
			backoff := time.Duration(3000*reconnect) * time.Millisecond
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
			util.LogWarn("直播流中断（%v），%v 后重连（%d/%d）...", err, backoff, reconnect, reconnectLimit)
			if !sleepCtx(ctx, backoff) {
				break
			}
			continue
		}
		if n > 0 {
			total += n
			reconnect = 0
			segFiles = append(segFiles, segPath)
		} else {
			// Zero bytes: re-check whether the streamer went offline.
			os.Remove(segPath)
			if _, _, _, err := ResolveLive(ctx, roomID, client); err != nil {
				if strings.Contains(err.Error(), "当前未在直播") {
					break
				}
			}
		}

		// Stream ended (EOF): confirm offline; keep recording if still live.
		if ctx.Err() != nil {
			break
		}
		if _, _, _, err := ResolveLive(ctx, roomID, client); err != nil {
			if strings.Contains(err.Error(), "当前未在直播") {
				break
			}
		}
	}

	if total == 0 {
		return false, nil
	}

	// Merge segments (single segment: plain rename; more: ffmpeg concat).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return true, err
	}
	if len(segFiles) == 1 {
		if err := os.Rename(segFiles[0], path); err != nil {
			return true, err
		}
		return true, nil
	}

	util.Log("正在合成 %d 个直播分段...", len(segFiles))
	listPath := filepath.Join(sessionDir, "concat.txt")
	var sb strings.Builder
	for _, f := range segFiles {
		sb.WriteString("file \"")
		sb.WriteString(strings.ReplaceAll(f, "\\", "/"))
		sb.WriteString("\"\n")
	}
	if err := os.WriteFile(listPath, []byte(sb.String()), 0o644); err != nil {
		return true, err
	}

	mergeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(mergeCtx, muxer.FFMPEG, "-loglevel", "warning", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return true, fmt.Errorf("直播分段合成失败（分段保留在 %s）: %w", sessionDir, err)
	}
	return true, nil
}

func streamToFile(ctx context.Context, url, segPath string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://live.bilibili.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(segPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, 1<<20) // 1MB buffer
	var offset int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return offset, werr
			}
			offset += int64(n)
		}
		if err == io.EOF {
			break // stream ended
		}
		if err != nil {
			return offset, err
		}
	}
	return offset, nil
}

// SanitizeFileName replaces invalid filename characters and control chars.
func SanitizeFileName(name string) string {
	invalid := []rune{'\\', '/', ':', '*', '?', '"', '<', '>', '|'}
	result := []rune(name)
	for i, r := range result {
		if r <= 31 {
			result[i] = '_'
			continue
		}
		for _, inv := range invalid {
			if r == inv {
				result[i] = '_'
				break
			}
		}
	}
	s := strings.TrimSpace(string(result))
	if s == "" {
		s = "直播"
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
