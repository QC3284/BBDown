package workflow

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

var (
	avRegex  = regexp.MustCompile(`[Aa][Vv](\d+)`)
	bvRegex  = regexp.MustCompile(`[Bb][Vv]1(\w+)`)
	epRegex  = regexp.MustCompile(`/ep(\d+)`)
	ssRegex  = regexp.MustCompile(`/ss(\d+)`)
	mdRegex  = regexp.MustCompile(`md(\d+)`)
	uidRegex = regexp.MustCompile(`space\.bilibili\.com/(\d+)`)
)

// ResolveURL resolves user input (URL, BV, AV, EP, SS, etc.) into a unified aid identifier.
func ResolveURL(ctx context.Context, client *util.HTTPClient, input string) (string, error) {
	// Already a known prefix
	if strings.HasPrefix(input, "cheese:") || strings.HasPrefix(input, "ep:") ||
		strings.HasPrefix(input, "mid:") || strings.HasPrefix(input, "favId:") ||
		strings.HasPrefix(input, "listBizId:") || strings.HasPrefix(input, "seriesBizId:") {
		return input, nil
	}

	// AV number
	if m := avRegex.FindStringSubmatch(input); m != nil {
		return "av" + m[1], nil
	}

	// BV string
	if m := bvRegex.FindStringSubmatch(input); m != nil {
		aid, err := entity.BvToAv(m[1])
		if err != nil {
			return input, nil // fallback
		}
		return strconv.FormatInt(aid, 10), nil
	}

	// EP in URL
	if m := epRegex.FindStringSubmatch(input); m != nil {
		return "ep:" + m[1], nil
	}

	// SS in URL — need to resolve to first EP
	if m := ssRegex.FindStringSubmatch(input); m != nil {
		return resolveSS(ctx, client, m[1])
	}

	// MD in URL — resolve to first EP
	if m := mdRegex.FindStringSubmatch(input); m != nil {
		return resolveMD(ctx, client, m[1])
	}

	// MID (user space)
	if m := uidRegex.FindStringSubmatch(input); m != nil {
		return "mid:" + m[1], nil
	}

	// Pure digits — check for AV redirect
	if _, err := strconv.ParseInt(input, 10, 64); err == nil {
		aid, err := fixAvid(ctx, client, input)
		if err != nil {
			return input, nil
		}
		return aid, nil
	}

	// Literal string — could be bilibili URL with aid in path
	if strings.Contains(input, "av") {
		if m := avRegex.FindStringSubmatch(input); m != nil {
			return "av" + m[1], nil
		}
	}

	return input, nil
}

func resolveSS(ctx context.Context, client *util.HTTPClient, ssID string) (string, error) {
	// Try PGC API first
	api := "https://api.bilibili.com/pgc/view/web/season?season_id=" + ssID
	resp, err := client.GetWebSource(ctx, api)
	if err == nil {
		// Quick regex parse for first ep_id
		m := epRegex.FindStringSubmatch(resp)
		if m != nil {
			return "ep:" + m[1], nil
		}
	}

	// Try PUGV (cheese) API
	api2 := "https://api.bilibili.com/pugv/view/web/season?season_id=" + ssID
	resp, err = client.GetWebSource(ctx, api2)
	if err == nil {
		m := epRegex.FindStringSubmatch(resp)
		if m != nil {
			return "cheese:" + m[1], nil
		}
	}

	return "", fmt.Errorf("无法解析 SS%s", ssID)
}

func resolveMD(ctx context.Context, client *util.HTTPClient, mdID string) (string, error) {
	api := "https://api.bilibili.com/pgc/review/user?media_id=" + mdID
	resp, err := client.GetWebSource(ctx, api)
	if err != nil {
		return "", fmt.Errorf("无法解析 MD%s: %w", mdID, err)
	}

	m := regexp.MustCompile(`"new_ep":\s*\{[^}]*"id":\s*(\d+)`).FindStringSubmatch(resp)
	if m != nil {
		return "ep:" + m[1], nil
	}

	return "", fmt.Errorf("无法解析 MD%s", mdID)
}

func fixAvid(ctx context.Context, client *util.HTTPClient, avid string) (string, error) {
	// Check if this avid redirects to bangumi (i.e., is actually an EP)
	api := "https://api.bilibili.com/x/web-interface/view?aid=" + avid
	resp, err := client.GetWebSource(ctx, api)
	if err != nil {
		return avid, nil
	}

	if strings.Contains(resp, `"redirect_url"`) && strings.Contains(resp, "bangumi") {
		// Extract ep ID from redirect_url
		m := regexp.MustCompile(`"redirect_url"\s*:\s*"[^"]*?ep(\d+)`).FindStringSubmatch(resp)
		if m != nil {
			return "ep:" + m[1], nil
		}
	}
	return avid, nil
}
