package live

import (
	"context"
	"encoding/json"
	"errors"
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

const (
	reconnectBaseBackoff = 3 * time.Second
	reconnectMaxBackoff  = 30 * time.Second
)

// readStallTimeout is a variable so tests can shrink it; production uses 60s.
var readStallTimeout = 60 * time.Second

// resolveLive is a seam for tests: DownloadToFile is otherwise untestable
// without hitting the live Bilibili API.
var resolveLive = ResolveLive

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
	// Only this session's directory is removed once the recording finished
	// cleanly; on a terminal failure the segments are kept for manual recovery
	// (upstream v1.6.13). The .segs root is never removed recursively — it may
	// still hold segments retained by earlier sessions.
	keepSegments := false
	defer func() {
		if keepSegments {
			return
		}
		os.RemoveAll(sessionDir)
		os.Remove(segRoot) // succeeds only when it is now empty
	}()

	var segFiles []string
	var total int64
	reconnect := 0
	segIdx := 0

	for {
		streamURL, _, _, err := resolveLive(ctx, roomID, client)
		if err != nil {
			// Live has ended: finish normally.
			if strings.Contains(err.Error(), "当前未在直播") {
				break
			}
			backoff := reconnectBackoff(reconnect)
			reconnect++
			util.LogWarn("直播流中断（%v），%v 后重连（第 %d 次）...", err, backoff, reconnect)
			if !sleepCtx(ctx, backoff) {
				break
			}
			continue
		}

		segPath := filepath.Join(sessionDir, fmt.Sprintf("seg-%03d.flv", segIdx))
		segIdx++
		n, err := streamToFile(ctx, streamURL, segPath)
		if n > 0 {
			// Keep whatever was written: a cancelled or interrupted segment is
			// still recoverable content, and it takes part in the merge.
			total += n
			segFiles = append(segFiles, segPath)
		}
		if err != nil {
			var werr errLiveWrite
			if errors.As(err, &werr) {
				// Local write failure is terminal — retrying cannot help.
				keepSegments = total > 0
				util.LogWarn("直播分段写入失败（%v），已录制内容保留在 %s", werr, sessionDir)
				return total > 0, fmt.Errorf("直播录制写盘失败: %w", werr)
			}
			if ctx.Err() != nil {
				break
			}
			backoff := reconnectBackoff(reconnect)
			reconnect++
			util.LogWarn("直播流中断（%v），%v 后重连（第 %d 次）...", err, backoff, reconnect)
			if !sleepCtx(ctx, backoff) {
				break
			}
			continue
		}
		if n > 0 {
			reconnect = 0
		} else {
			// Zero bytes: re-check whether the streamer went offline.
			os.Remove(segPath)
			if _, _, _, err := resolveLive(ctx, roomID, client); err != nil {
				if strings.Contains(err.Error(), "当前未在直播") {
					break
				}
			}
		}

		// Stream ended (EOF): confirm offline; keep recording if still live.
		if ctx.Err() != nil {
			break
		}
		if _, _, _, err := resolveLive(ctx, roomID, client); err != nil {
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

	// Segment merging reuses the muxer timeout ceiling (default 30 minutes).
	mergeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(mergeCtx, muxer.FFMPEG, "-loglevel", "warning", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		keepSegments = true
		return true, fmt.Errorf("直播分段合成失败（分段保留在 %s）: %w", sessionDir, err)
	}
	return true, nil
}

// errLiveWrite marks a local write failure: unlike a network interruption it
// is terminal and must not be retried (upstream LiveStreamWriteException).
type errLiveWrite struct{ err error }

func (e errLiveWrite) Error() string { return e.err.Error() }
func (e errLiveWrite) Unwrap() error { return e.err }

// reconnectBackoff grows from a few seconds up to a ceiling. Reconnection is
// unlimited (upstream v1.6.13): as long as the room is still broadcasting and
// the user has not cancelled, a lost network must not end the recording.
func reconnectBackoff(attempt int) time.Duration {
	d := time.Duration(attempt+1) * reconnectBaseBackoff
	if d > reconnectMaxBackoff {
		return reconnectMaxBackoff
	}
	return d
}

// streamToFile writes one segment to disk. A read that stops producing data for
// readStallTimeout is treated as an interruption: during a network black hole
// the connection neither resets nor EOFs, and the body read would otherwise
// block forever, hanging the whole recording. Bytes already written are
// returned even on error so the caller can keep them.
func streamToFile(ctx context.Context, url, segPath string) (int64, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stall := time.AfterFunc(readStallTimeout, cancel)
	defer stall.Stop()

	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
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
		return 0, errLiveWrite{err}
	}
	defer f.Close()

	buf := make([]byte, 1<<20) // 1MB buffer
	var offset int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(readStallTimeout)
			if _, werr := f.Write(buf[:n]); werr != nil {
				return offset, errLiveWrite{werr}
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
