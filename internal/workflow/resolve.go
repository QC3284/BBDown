package workflow

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

var (
	avRegex     = regexp.MustCompile("[Aa][Vv](\\d+)")
	bvRegex     = regexp.MustCompile("[Bb][Vv]1(\\w+)")
	epRegex     = regexp.MustCompile("/ep(\\d+)")
	ssRegex     = regexp.MustCompile("/ss(\\d+)")
	mdRegex     = regexp.MustCompile("md(\\d+)")
	uidRegex    = regexp.MustCompile("space\\.bilibili\\.com/(\\d+)")
	epIDParam   = regexp.MustCompile("[?&]ep_id=(\\d+)")
	tvPlayRegex = regexp.MustCompile("bilibili\\.tv/\\w+/play/(\\d+)/(\\d+)")
	epListRegex = regexp.MustCompile("\"epList\"\\s*:\\s*\\[\\s*\\{[^{]*?\"id\"\\s*:\\s*(\\d+)")
	newEpRegex  = regexp.MustCompile("\"new_ep\"\\s*:\\s*\\{[^}]*\"id\"\\s*:\\s*(\\d+)")
	redirectEp  = regexp.MustCompile("\"redirect_url\"\\s*:\\s*\"[^\"]*?ep(\\d+)")
	favlistFid  = regexp.MustCompile("favlist\\?fid=(\\d+)")
	listsSid    = regexp.MustCompile("/lists/(\\d+)")
)

// knownPrefixes are the unified identifiers passed through unchanged.
var knownPrefixes = []string{"cheese:", "ep:", "mid:", "favId:", "listBizId:", "seriesBizId:"}

// ResolveURL resolves user input (URL, BV, AV, EP, SS, MD, space/favlist/medialist
// links, b23.tv short links) into a unified aid identifier, mirroring upstream
// UrlResolver. Invalid BV input is an error (upstream throws).
func ResolveURL(ctx context.Context, client *util.HTTPClient, input string) (string, error) {
	input = strings.TrimSpace(input)

	for _, p := range knownPrefixes {
		if strings.HasPrefix(input, p) {
			return input, nil
		}
	}

	lower := strings.ToLower(input)

	// b23.tv short link: follow redirects, then re-resolve the final URL.
	if hostOf(input) == "b23.tv" {
		final, err := client.GetWebLocation(ctx, input)
		if err != nil || final == input || final == "" {
			if err != nil {
				return "", fmt.Errorf("解析短链失败: %w", err)
			}
			return "", fmt.Errorf("解析短链失败：无法获取重定向目标")
		}
		return ResolveURL(ctx, client, final)
	}

	// Bare "av:" / "bv:" prefixed identifiers.
	if strings.HasPrefix(lower, "av:") {
		return normalizeAvDigits(strings.TrimPrefix(input, "av:"))
	}
	if strings.HasPrefix(lower, "bv:") {
		return resolveBv(strings.TrimPrefix(input, "bv:"))
	}

	// AV number (bare "av123" or URL containing av\d+).
	if strings.HasPrefix(lower, "av") {
		rest := input[2:]
		if _, err := strconv.ParseInt(rest, 10, 64); err == nil {
			return normalizeAvDigits(rest)
		}
	}
	if m := avRegex.FindStringSubmatch(input); m != nil {
		return normalizeAvDigits(m[1])
	}

	// BV string.
	if m := bvRegex.FindStringSubmatch(input); m != nil {
		return resolveBv(m[1])
	}

	// Bare ep / ss / md identifiers.
	if strings.HasPrefix(lower, "ep") {
		return "ep:" + strings.TrimLeft(input[2:], ":"), nil
	}
	if strings.HasPrefix(lower, "ss") {
		return resolveSS(ctx, client, strings.TrimLeft(input[2:], ":"))
	}
	if strings.HasPrefix(lower, "md") {
		mdID := strings.TrimLeft(input[2:], ":")
		if mdID == "" {
			return "", fmt.Errorf("输入有误：无法识别的 MD 号")
		}
		return resolveMD(ctx, client, mdID)
	}

	// Cheese course URLs (/cheese/ep123 or /cheese/ss123).
	if strings.Contains(lower, "/cheese/") {
		if m := regexp.MustCompile("/cheese/ep(\\d+)").FindStringSubmatch(lower); m != nil {
			return "cheese:" + m[1], nil
		}
		if m := regexp.MustCompile("/cheese/ss(\\d+)").FindStringSubmatch(lower); m != nil {
			epID, err := resolvePugvFirstEp(ctx, client, m[1])
			if err != nil {
				return "", err
			}
			return "cheese:" + epID, nil
		}
	}

	// EP / SS / MD in URL.
	if m := epRegex.FindStringSubmatch(input); m != nil {
		return "ep:" + m[1], nil
	}
	if m := ssRegex.FindStringSubmatch(input); m != nil {
		return resolveSS(ctx, client, m[1])
	}
	if m := mdRegex.FindStringSubmatch(input); m != nil {
		return resolveMD(ctx, client, m[1])
	}

	// ep_id query parameter.
	if m := epIDParam.FindStringSubmatch(input); m != nil {
		return "ep:" + m[1], nil
	}

	// bilibili.tv play page.
	if m := tvPlayRegex.FindStringSubmatch(input); m != nil {
		return "ep:" + m[2], nil
	}

	// Channel collection/series URLs (before the space mid branch: these URLs
	// also live under space.bilibili.com/{mid}/...). Upstream uses order-
	// independent substring checks + query extraction.
	if strings.Contains(input, "/channel/collectiondetail?sid=") {
		return "listBizId:" + queryParam(input, "sid"), nil
	}
	if strings.Contains(input, "/channel/seriesdetail?sid=") {
		return "seriesBizId:" + queryParam(input, "sid"), nil
	}
	if strings.Contains(input, "/medialist/") && strings.Contains(input, "business_id=") && strings.Contains(input, "business=space_collection") {
		return "listBizId:" + queryParam(input, "business_id"), nil
	}
	if strings.Contains(input, "/medialist/") && strings.Contains(input, "business_id=") && strings.Contains(input, "business=space_series") {
		return "seriesBizId:" + queryParam(input, "business_id"), nil
	}

	// User space / favlist / lists URLs.
	if m := uidRegex.FindStringSubmatch(input); m != nil {
		mid := m[1]
		if mf := favlistFid.FindStringSubmatch(input); mf != nil {
			return fmt.Sprintf("favId:%s:%s", mf[1], mid), nil
		}
		if ml := listsSid.FindStringSubmatch(input); ml != nil {
			if strings.Contains(lower, "type=series") {
				return "seriesBizId:" + ml[1], nil
			}
			return "listBizId:" + ml[1], nil
		}
		return "mid:" + mid, nil
	}

	// Pure digits — check for bangumi redirect.
	if _, err := strconv.ParseInt(input, 10, 64); err == nil {
		return fixAvid(ctx, client, input)
	}

	// Unknown URL: try to resolve from the page __INITIAL_STATE__ (trusted hosts only).
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if !isTrustedHost(hostOf(input)) {
			return "", fmt.Errorf("输入有误：无法识别的链接（不信任的域名 %s）", hostOf(input))
		}
		source, err := client.GetWebSource(ctx, input)
		if err == nil {
			if m := epListRegex.FindStringSubmatch(source); m != nil {
				return "ep:" + m[1], nil
			}
		}
		return "", fmt.Errorf("输入有误：无法从页面解析出目标视频")
	}

	return input, nil
}

// normalizeAvDigits validates a pure-digit av string.
func normalizeAvDigits(digits string) (string, error) {
	if _, err := strconv.ParseInt(digits, 10, 64); err != nil {
		return "", fmt.Errorf("输入有误：AV 号格式不正确: %s", digits)
	}
	return digits, nil
}

// resolveBv converts a BV suffix to a numeric aid; invalid input is an error.
func resolveBv(bvSuffix string) (string, error) {
	aid, err := entity.BvToAv(bvSuffix)
	if err != nil {
		return "", fmt.Errorf("输入有误：BV 号格式不正确")
	}
	return strconv.FormatInt(aid, 10), nil
}

func resolveSS(ctx context.Context, client *util.HTTPClient, ssID string) (string, error) {
	// Try PGC API first (bangumi).
	api := "https://api.bilibili.com/pgc/view/web/season?season_id=" + ssID
	resp, err := client.GetWebSource(ctx, api)
	if err == nil {
		if m := epRegex.FindStringSubmatch(resp); m != nil {
			return "ep:" + m[1], nil
		}
	}

	// Fallback to PUGV (cheese) API.
	api2 := "https://api.bilibili.com/pugv/view/web/season?season_id=" + ssID
	resp, err = client.GetWebSource(ctx, api2)
	if err == nil {
		if m := epRegex.FindStringSubmatch(resp); m != nil {
			return "cheese:" + m[1], nil
		}
	}

	return "", fmt.Errorf("无法解析 SS%s", ssID)
}

// resolvePugvFirstEp resolves the first episode id of a cheese season.
func resolvePugvFirstEp(ctx context.Context, client *util.HTTPClient, ssID string) (string, error) {
	api := "https://api.bilibili.com/pugv/view/web/season?season_id=" + ssID
	resp, err := client.GetWebSource(ctx, api)
	if err != nil {
		return "", fmt.Errorf("无法解析 SS%s: %w", ssID, err)
	}
	m := epRegex.FindStringSubmatch(resp)
	if m == nil {
		return "", fmt.Errorf("无法解析 SS%s", ssID)
	}
	return m[1], nil
}

func resolveMD(ctx context.Context, client *util.HTTPClient, mdID string) (string, error) {
	api := "https://api.bilibili.com/pgc/review/user?media_id=" + mdID
	resp, err := client.GetWebSource(ctx, api)
	if err != nil {
		return "", fmt.Errorf("无法解析 MD%s: %w", mdID, err)
	}

	m := newEpRegex.FindStringSubmatch(resp)
	if m != nil {
		return "ep:" + m[1], nil
	}

	return "", fmt.Errorf("无法解析 MD%s", mdID)
}

// fixAvid checks whether a numeric aid redirects to bangumi (i.e. is an EP) and
// normalizes accordingly. Network failures keep the original aid (upstream).
func fixAvid(ctx context.Context, client *util.HTTPClient, avid string) (string, error) {
	api := "https://api.bilibili.com/x/web-interface/view?aid=" + avid
	resp, err := client.GetWebSource(ctx, api)
	if err != nil {
		return avid, nil
	}

	if strings.Contains(resp, "\"redirect_url\"") && strings.Contains(resp, "bangumi") {
		m := redirectEp.FindStringSubmatch(resp)
		if m != nil {
			return "ep:" + m[1], nil
		}
	}
	return avid, nil
}

// queryParam extracts a query parameter value from a URL.
func queryParam(raw, key string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}

// hostOf returns the host of a URL, or "" if not a URL.
func hostOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// isTrustedHost reports whether a host belongs to a Bilibili family domain.
func isTrustedHost(host string) bool {
	for _, suffix := range []string{"bilibili.com", "b23.tv", "biliintl.com", "bilibili.tv", "bilivideo.com", "hdslb.com", "aisee.tv", "biliapi.net"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
