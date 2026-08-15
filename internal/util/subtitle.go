package util

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
)

// GetSubtitles fetches subtitles with multi-API fallback matching C#.
func GetSubtitles(ctx context.Context, client *HTTPClient, aid, cid, epid string, index int, intl bool, cookie string) ([]entity.Subtitle, error) {
	var subtitles []entity.Subtitle

	if intl {
		subtitles = getIntlSubAPI1(ctx, client, aid, cid, epid)
		if subtitles == nil {
			subtitles = getIntlSubAPI2(ctx, client, aid, cid, epid, index)
		}
	} else {
		if cookie == "" {
			subtitles = getSubAPI3(ctx, client, aid, cid)
		} else {
			subtitles = getSubAPI2(ctx, client, aid, cid)
			if subtitles == nil {
				subtitles = getSubAPI1(ctx, client, aid, cid)
			}
			if subtitles == nil {
				subtitles = getSubAPI3(ctx, client, aid, cid)
			}
		}
	}
	if subtitles == nil {
		return nil, nil
	}
	for i := range subtitles {
		if strings.HasPrefix(subtitles[i].URL, "//") {
			subtitles[i].URL = "https:" + subtitles[i].URL
		}
	}
	return subtitles, nil
}

func getSubAPI1(ctx context.Context, client *HTTPClient, aid, cid string) []entity.Subtitle {
	resp, err := client.GetWebSource(ctx, fmt.Sprintf("https://api.bilibili.com/x/web-interface/view?aid=%s&cid=%s", aid, cid))
	if err != nil {
		return nil
	}
	var r struct {
		Data struct {
			Subtitle struct {
				List []struct {
					Lan         string `json:"lan"`
					SubtitleURL string `json:"subtitle_url"`
				} `json:"list"`
			} `json:"subtitle"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(resp), &r) != nil {
		return nil
	}
	var subs []entity.Subtitle
	for _, s := range r.Data.Subtitle.List {
		if s.SubtitleURL == "" {
			continue
		}
		subs = append(subs, entity.Subtitle{
			Lan:  s.Lan,
			URL:  s.SubtitleURL,
			Path: fmt.Sprintf("%s/%s.%s.%s.srt", aid, aid, cid, s.Lan),
		})
	}
	return subs
}

func getSubAPI2(ctx context.Context, client *HTTPClient, aid, cid string) []entity.Subtitle {
	resp, err := client.GetWebSource(ctx, fmt.Sprintf("https://api.bilibili.com/x/player/wbi/v2?cid=%s&aid=%s", cid, aid))
	if err != nil {
		return nil
	}
	var r struct {
		Data struct {
			Subtitle struct {
				Subtitles []struct {
					Lan         string `json:"lan"`
					SubtitleURL string `json:"subtitle_url"`
				} `json:"subtitles"`
			} `json:"subtitle"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(resp), &r) != nil {
		return nil
	}
	var subs []entity.Subtitle
	for _, s := range r.Data.Subtitle.Subtitles {
		if s.SubtitleURL == "" {
			continue
		}
		subs = append(subs, entity.Subtitle{
			Lan:  s.Lan,
			URL:  s.SubtitleURL,
			Path: fmt.Sprintf("%s/%s.%s.%s.srt", aid, aid, cid, s.Lan),
		})
	}
	return subs
}

func getSubAPI3(ctx context.Context, client *HTTPClient, aid, cid string) []entity.Subtitle {
	resp, err := client.GetWebSource(ctx, fmt.Sprintf("https://api.bilibili.com/x/player/v2?cid=%s&aid=%s", cid, aid))
	if err != nil {
		return nil
	}
	var r struct {
		Data struct {
			Subtitle struct {
				Subtitles []struct {
					Lan         string `json:"lan"`
					SubtitleURL string `json:"subtitle_url"`
				} `json:"subtitles"`
			} `json:"subtitle"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(resp), &r) != nil {
		return nil
	}
	var subs []entity.Subtitle
	for _, s := range r.Data.Subtitle.Subtitles {
		if s.SubtitleURL == "" {
			continue
		}
		subs = append(subs, entity.Subtitle{
			Lan:  s.Lan,
			URL:  s.SubtitleURL,
			Path: fmt.Sprintf("%s/%s.%s.%s.srt", aid, aid, cid, s.Lan),
		})
	}
	return subs
}

func getIntlSubAPI1(ctx context.Context, client *HTTPClient, aid, cid, epid string) []entity.Subtitle {
	resp, err := client.GetWebSource(ctx, fmt.Sprintf("https://api.biliintl.com/intl/gateway/web/v2/subtitle?episode_id=%s", epid))
	if err != nil {
		return nil
	}
	var r struct {
		Data struct {
			Subtitles []struct {
				LangKey string `json:"lang_key"`
				URL     string `json:"url"`
			} `json:"subtitles"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(resp), &r) != nil {
		return nil
	}
	var subs []entity.Subtitle
	for _, s := range r.Data.Subtitles {
		if s.URL == "" {
			continue
		}
		ext := ".ass"
		if strings.Contains(s.URL, ".json") {
			ext = ".srt"
		}
		subs = append(subs, entity.Subtitle{
			Lan:  s.LangKey,
			URL:  s.URL,
			Path: fmt.Sprintf("%s/%s.%s.%s%s", aid, aid, cid, s.LangKey, ext),
		})
	}
	return subs
}

func getIntlSubAPI2(ctx context.Context, client *HTTPClient, aid, cid, epid string, index int) []entity.Subtitle {
	resp, err := client.GetWebSource(ctx, fmt.Sprintf("https://api.bilibili.tv/intl/gateway/v2/ogv/view/app/season?ep_id=%s&platform=android&s_locale=zh_SG", epid))
	if err != nil {
		return nil
	}
	var r struct {
		Result struct {
			Modules []struct {
				Data struct {
					Episodes []struct {
						Subtitles []struct {
							Key string `json:"key"`
							URL string `json:"url"`
						} `json:"subtitles"`
					} `json:"episodes"`
				} `json:"data"`
			} `json:"modules"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(resp), &r) != nil || len(r.Result.Modules) == 0 || len(r.Result.Modules[0].Data.Episodes) == 0 {
		return nil
	}
	eps := r.Result.Modules[0].Data.Episodes
	if index < 1 || index > len(eps) {
		return nil
	}
	var subs []entity.Subtitle
	for _, s := range eps[index-1].Subtitles {
		if s.URL == "" {
			continue
		}
		u := strings.ReplaceAll(s.URL, "\\/", "/")
		ext := ".ass"
		if strings.Contains(u, ".json") {
			ext = ".srt"
		}
		subs = append(subs, entity.Subtitle{
			Lan:  s.Key,
			URL:  u,
			Path: fmt.Sprintf("%s/%s.%s.%s%s", aid, aid, cid, s.Key, ext),
		})
	}
	return subs
}

// SaveSubtitle downloads and saves a subtitle, converting JSON to SRT if needed.
func SaveSubtitle(client *HTTPClient, url, path string) error {
	body, err := client.GetWebSource(context.Background(), url)
	if err != nil {
		return err
	}
	if strings.HasSuffix(path, ".srt") {
		body = ConvertSubFromJSON(body)
	}
	return os.WriteFile(path, []byte(body), 0644)
}

// ConvertSubFromJSON converts Bilibili JSON subtitle format to SRT.
func ConvertSubFromJSON(jsonStr string) string {
	var data struct {
		Body []struct {
			From    float64 `json:"from"`
			To      float64 `json:"to"`
			Content string  `json:"content"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return ""
	}
	var sb strings.Builder
	for i, line := range data.Body {
		sb.WriteString(fmt.Sprintf("%d\n", i+1))
		sb.WriteString(fmt.Sprintf("%s --> %s\n", FormatSubTime(line.From), FormatSubTime(line.To)))
		sb.WriteString(SanitizeSRT(line.Content) + "\n\n")
	}
	return sb.String()
}

// FormatSubTime formats seconds to SRT timestamp.
func FormatSubTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := int(sec) % 60
	ms := int((sec - float64(int(sec))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// SanitizeSRT cleans content for valid SRT format.
func SanitizeSRT(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	var kept []string
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if len(l) > 0 {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// GetSubtitleCode / SubCode2 now live in sublang.go (full upstream table).
