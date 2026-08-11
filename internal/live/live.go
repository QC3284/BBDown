package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

const reconnectLimit = 3

// ResolveLive resolves a Bilibili live room ID to a stream URL and metadata.
func ResolveLive(roomID string, client *util.HTTPClient) (url, title, uname string, err error) {
	if _, err := strconv.ParseInt(roomID, 10, 64); err != nil {
		return "", "", "", fmt.Errorf("直播间 ID 必须是数字，当前值: '%s'", roomID)
	}

	// Get room info
	infoAPI := "https://api.live.bilibili.com/room/v1/Room/get_info?room_id=" + roomID
	infoJSON, err := client.GetWebSource(nil, infoAPI)
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
	playJSON, err := client.GetWebSource(nil, playAPI)
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
					host := urlInfo.Host
					extra := urlInfo.Extra
					if host == "" {
						continue
					}
					return host + baseURL + extra, title, uname, nil
				}
			}
		}
	}

	return "", "", "", fmt.Errorf("无法获取直播间 %s 的可录制流地址", roomID)
}

// DownloadToFile downloads a live stream to a local file with automatic reconnection.
func DownloadToFile(roomID, path string, client *util.HTTPClient) error {
	partPath := path + ".part"
	// Clean up previous partial
	os.Remove(partPath)

	var total int64
	reconnect := 0

	for {
		streamURL, _, _, err := ResolveLive(roomID, client)
		if err != nil {
			// If live has ended (not streaming), exit normally
			if strings.Contains(err.Error(), "当前未在直播") {
				break
			}
			reconnect++
			if reconnect > reconnectLimit {
				util.LogWarn("直播流中断且 %d 次重连失败，已录制内容保留在 %s", reconnectLimit, partPath)
				return fmt.Errorf("重连失败: %w", err)
			}
			util.LogWarn("直播流中断（%v），3 秒后重连（%d/%d）...", err, reconnect, reconnectLimit)
			time.Sleep(3 * time.Second)
			continue
		}

		reconnect = 0 // Reset on successful reconnection

		total, err = streamToFile(streamURL, partPath, total)
		if err != nil {
			reconnect++
			if reconnect > reconnectLimit {
				util.LogWarn("直播流中断且 %d 次重连失败，已录制内容保留在 %s", reconnectLimit, partPath)
				return fmt.Errorf("流传输失败: %w", err)
			}
			util.LogWarn("直播流中断（%v），3 秒后重连（%d/%d）...", err, reconnect, reconnectLimit)
			time.Sleep(3 * time.Second)
			continue
		}
		break // Stream ended normally (streamer went offline)
	}

	// Atomically rename .part to final path
	if _, err := os.Stat(partPath); err == nil {
		os.Rename(partPath, path)
	}
	return nil
}

func streamToFile(url, partPath string, offset int64) (int64, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return offset, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://live.bilibili.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return offset, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return offset, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(partPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return offset, err
	}
	defer f.Close()

	buf := make([]byte, 1<<20) // 1MB buffer
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

// SanitizeFileName replaces invalid filename characters.
func SanitizeFileName(name string) string {
	invalid := []rune{'\\', '/', ':', '*', '?', '"', '<', '>', '|'}
	result := []rune(name)
	for i, r := range result {
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
