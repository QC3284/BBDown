package parser

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/appapi"
	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// isVipRestricted reports whether a playurl response is the 大会员专享限制 business
// error. The JSON root "message" is authoritative; a raw substring check only
// backs up non-JSON bodies, so a wording or encoding change cannot silently
// disable the web-page fallback (upstream IsVipRestrictedResponse).
func isVipRestricted(jsonStr string) bool {
	var doc map[string]interface{}
	if util.UnmarshalJSON(jsonStr, &doc) == nil {
		if msg, ok := doc["message"].(string); ok && msg != "" {
			return strings.Contains(msg, "大会员专享限制")
		}
	}
	return strings.Contains(jsonStr, "大会员专享限制")
}

// throwIfPlayLimited 对应上游 Parser.ThrowIfPlayLimited：result.play_check 里给了
// limit_play_reason / play_detail 就说明接口判定不可播放，按原因给出可读文案
// （只说「播放受限」会让用户无法判断是区域、付费、大会员还是未到时间）。
func throwIfPlayLimited(root map[string]interface{}) error {
	resultData, ok := root["result"].(map[string]interface{})
	if !ok {
		return nil
	}
	playCheck, ok := resultData["play_check"].(map[string]interface{})
	if !ok {
		return nil
	}
	reason, _ := playCheck["limit_play_reason"].(string)
	detail, _ := playCheck["play_detail"].(string)
	if strings.TrimSpace(reason) == "" && strings.TrimSpace(detail) == "" {
		return nil
	}
	var message string
	switch reason {
	case "AREA_LIMIT":
		message = "当前番剧/视频存在区域限制，接口返回不可播放"
	case "PAY_LIMIT":
		message = "当前番剧/视频存在付费限制，接口返回不可播放"
	case "VIP_LIMIT":
		message = "当前番剧/视频需要大会员权限，接口返回不可播放"
	case "TIME_LOCK":
		message = "当前番剧/视频尚未到可播放时间，接口返回不可播放"
	default:
		message = "当前番剧/视频存在播放限制，接口返回不可播放"
	}
	return fmt.Errorf("%s (limit_play_reason=%s, play_detail=%s)", message, reason, detail)
}

// throwIfBizError 对应上游 Parser.ThrowIfBizError：只在根是对象、且 code 是**JSON 数字**
// 且非 0 时才报错——字符串形态的 code（如 "-412"）按上游语义不算业务错误。
func throwIfBizError(root map[string]interface{}) error {
	code, ok := root["code"].(float64)
	if !ok || code == 0 {
		return nil
	}
	msg, _ := root["message"].(string)
	if msg == "" {
		msg = fmt.Sprintf("接口返回错误码 %d", int64(code))
	}
	return fmt.Errorf("接口返回错误: %s (code=%d)", msg, int64(code))
}

// shouldExtractDubbing 是上游对 dubbing_info 的门控：只有 APP API + 番剧（ep/cheese）
// 场景的响应里才有这两类轨道，其它路径里出现同名节点不能当数据用。
func shouldExtractDubbing(appAPI bool, aidOri string) bool {
	return appAPI && (strings.HasPrefix(aidOri, "ep:") || strings.HasPrefix(aidOri, "cheese:"))
}

// extractDubbingInfo 读取 data.dubbing_info 下的背景音频与配音（上游 Parser.cs 的同一段，
// 仅在 APP API + 番剧场景下出现）。此前本仓完全没有这段解析：工作流虽然写了背景音轨/配音
// 的下载循环，但两个列表永远为空，等于死代码。
func extractDubbingInfo(data map[string]interface{}, result *entity.ParsedResult, aid, cid string, pDur int) {
	dub, ok := data["dubbing_info"].(map[string]interface{})
	if !ok {
		return
	}
	bgArr, hasBg := dub["background_audio"].([]interface{})
	roleArr, hasRole := dub["role_audio_list"].([]interface{})
	if !hasBg || !hasRole {
		return
	}

	toAudio := func(node map[string]interface{}) entity.Audio {
		audioID := getString(node, "id")
		urlList := []string{getString(node, "base_url")}
		if backups, ok := node["backup_url"].([]interface{}); ok {
			for _, b := range backups {
				if bs, ok := b.(string); ok {
					urlList = append(urlList, bs)
				}
			}
		}
		finalURL := urlList[0]
		for _, u := range urlList {
			if !baseURLRegex.MatchString(u) {
				finalURL = u
				break
			}
		}
		return entity.Audio{
			ID:        audioID,
			Dfn:       audioID,
			Dur:       pDur,
			Bandwidth: getInt64(node, "bandwidth") / 1000,
			BaseURL:   finalURL,
			Codecs:    getString(node, "codecs"),
		}
	}

	for _, n := range bgArr {
		node, ok := n.(map[string]interface{})
		if !ok {
			continue
		}
		result.BackgroundAudioTracks = append(result.BackgroundAudioTracks, toAudio(node))
	}

	for _, r := range roleArr {
		role, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		material := entity.AudioMaterialInfo{
			AudioID:    getString(role, "audio_id"),
			Title:      getString(role, "title"),
			PersonName: getString(role, "person_name"),
		}
		if arr, ok := role["audio"].([]interface{}); ok {
			for _, n := range arr {
				node, ok := n.(map[string]interface{})
				if !ok {
					continue
				}
				material.Audio = append(material.Audio, toAudio(node))
			}
		}
		if len(material.Audio) == 0 {
			continue
		}
		// 产物路径与上游同构：<aid>/<aid>.<cid>.<净化后的 audio_id>.m4a
		seg := util.SanitizePathSegment(material.AudioID)
		if seg == "" {
			seg = util.SanitizePathSegment(material.Audio[0].ID)
		}
		if seg == "" {
			seg = "audio"
		}
		material.Path = path.Join(aid, fmt.Sprintf("%s.%s.%s.m4a", aid, cid, seg))
		result.RoleAudioList = append(result.RoleAudioList, material)
	}
}

// apiBase returns the scheme-qualified base URL for an API host. A host that
// already carries a scheme is used as-is (upstream WithApiScheme), which is what
// lets --host point at a plain-http mirror or a local test server.
func apiBase(host string) string {
	h := strings.TrimRight(strings.TrimSpace(host), "/")
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		return h
	}
	if h == "" {
		return "https://api.bilibili.com"
	}
	return "https://" + h
}

// parseKidFromDrmURI extracts the key id from a bilidrm:// URI. Only a bare
// 32-hex value is accepted: a malformed URI carrying a host, path or query must
// not be mistaken for a key id (the upstream drm-dash-badkid fixture expects no kid).
func parseKidFromDrmURI(uri string) string {
	rest := uri
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) != 32 {
		return ""
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return ""
	}
	return rest
}

// baseURLRegex matches a direct host URL carrying an explicit port. It must be
// anchored: the previous `http.*:\d+` also matched a ':port' appearing inside a
// query string, which picked the wrong base_url (upstream tightened it to
// ^https?://[^/:]+:\d+).
var baseURLRegex = regexp.MustCompile(`^https?://[^/:]+:\d+`)
var playerJSONRegex = regexp.MustCompile(`window\.__playinfo__=([\s\S]*?)<\/script>`)

// Parser handles video stream URL extraction.
type Parser struct {
	HTTPClient *util.HTTPClient
	Cfg        config.AppSettings
}

// NewParser creates a new Parser.
func NewParser(client *util.HTTPClient, cfg config.AppSettings) *Parser {
	return &Parser{HTTPClient: client, Cfg: cfg}
}

// WbiSign generates a Wbi-signed API url.
func (p *Parser) WbiSign(api string) string {
	h := md5.Sum([]byte(api + p.Cfg.Wbi))
	return api + "&w_rid=" + hex.EncodeToString(h[:])
}

// VideoCodec converts codec ID to name.
func VideoCodec(code string) string {
	switch code {
	case "13":
		return "AV1"
	case "12":
		return "HEVC"
	case "7":
		return "AVC"
	default:
		return "UNKNOWN"
	}
}

func timeStamp() string {
	// Corrected by the observed server clock offset: a drifted local clock makes
	// the signed request look stale and the API rejects it.
	return strconv.FormatInt(util.UnixNow(), 10)
}

// ExtractTracks parses video/audio track information from Bilibili playurl API.
func (p *Parser) ExtractTracks(ctx context.Context, aidOri, aid, cid, epid string, tvAPI, intlAPI, appAPI bool, encoding string, wantDrm bool, qn string) (*entity.ParsedResult, error) {
	if qn == "" {
		qn = "0"
	}

	result := &entity.ParsedResult{}

	jsonStr, err := p.getPlayJSON(ctx, encoding, aidOri, aid, cid, epid, tvAPI, intlAPI, &appAPI, wantDrm, qn)
	if err != nil {
		return nil, err
	}
	result.WebJSONString = jsonStr

	// 免二压重发 (upstream ParseDash, reparsePass 1): the first pass asks for
	// qn=0, which can omit tracks the maximum tier would offer. A second pass at
	// qn=127 is issued for every non-APP, non-INTL request; only a document that
	// actually carries dash.video takes over — a refusal or a failure leaves the
	// first document in place ("降级沿用第一轮结果").
	if !intlAPI && !appAPI {
		better, err := p.reparseMaxQn(ctx, encoding, aidOri, aid, cid, epid, tvAPI, wantDrm)
		if err != nil {
			return nil, err
		}
		if better != "" {
			jsonStr = better
			result.WebJSONString = better
		}
	}

	// Handle intl API (two-pass: code=0 and code=1)
	if intlAPI {
		return p.parseIntlStreams(ctx, result, aid, cid, epid, qn)
	}

	return p.parseDomesticStreams(ctx, result, aidOri, aid, cid, epid, tvAPI, appAPI, encoding, wantDrm)
}

// maxQn is the highest quality id the API accepts (upstream GetMaxQn).
const maxQn = "127"

// reparseMaxQn re-requests the playurl document at the maximum quality and
// returns it only when it may take over: the document must parse and carry a
// non-empty dash.video list. An empty string with a nil error means "keep the
// first pass"; a non-nil error is a user cancellation, which must propagate
// rather than be downgraded into the first response (upstream RF-17).
func (p *Parser) reparseMaxQn(ctx context.Context, encoding, aidOri, aid, cid, epid string, tvAPI, wantDrm bool) (string, error) {
	appAPI := false
	next, err := p.getPlayJSON(ctx, encoding, aidOri, aid, cid, epid, tvAPI, false, &appAPI, wantDrm, maxQn)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		util.LogDebug("免二压重新请求失败（降级沿用第一轮结果）: %v", err)
		return "", nil
	}
	if !hasDashVideo(next) {
		util.LogDebug("免二压重新请求未返回轨道（降级沿用第一轮结果）")
		return "", nil
	}
	return next, nil
}

// hasDashVideo reports whether a playurl document offers a usable DASH video
// list, walking the same root variants the parser accepts (result.video_info,
// result, data, or the document itself).
func hasDashVideo(jsonStr string) bool {
	var doc map[string]interface{}
	if util.UnmarshalJSON(jsonStr, &doc) != nil {
		return false
	}
	roots := []map[string]interface{}{doc}
	if res, ok := doc["result"].(map[string]interface{}); ok {
		if vi, ok := res["video_info"].(map[string]interface{}); ok {
			roots = append([]map[string]interface{}{vi}, roots...)
		}
		roots = append([]map[string]interface{}{res}, roots...)
	}
	if data, ok := doc["data"].(map[string]interface{}); ok {
		roots = append([]map[string]interface{}{data}, roots...)
	}
	for _, r := range roots {
		dash, ok := r["dash"].(map[string]interface{})
		if !ok {
			continue
		}
		if videos, ok := dash["video"].([]interface{}); ok && len(videos) > 0 {
			return true
		}
	}
	return false
}

func (p *Parser) getPlayJSON(ctx context.Context, encoding, aidOri, aid, cid, epid string, tvAPI, intlAPI bool, appAPI *bool, wantDrm bool, qn string) (string, error) {
	if intlAPI {
		return p.getIntlPlayJSON(ctx, aid, cid, epid, qn, "0")
	}

	cheese := strings.HasPrefix(aidOri, "cheese:")
	bangumi := cheese || strings.HasPrefix(aidOri, "ep:")

	if *appAPI {
		// APP gRPC playurl (upstream AppHelper.DoReqAsync).
		jsonStr, err := appapi.DoReq(ctx, p.HTTPClient, aid, cid, epid, qn, bangumi, encoding, p.Cfg.Token)
		if err != nil {
			return "", err
		}
		util.LogDebug("APP API 响应已获取")
		return jsonStr, nil
	}

	var api string
	prefix := ""

	if tvAPI {
		base := apiBase(p.Cfg.TvHost)
		if bangumi {
			prefix = base + "/pgc/player/api/playurltv?"
		} else {
			prefix = base + "/x/tv/playurl?"
		}
	} else {
		base := apiBase(p.Cfg.Host)
		if bangumi {
			prefix = base + "/pgc/player/web/v2/playurl?"
		} else {
			// Cfg.Host, not a hardcoded api.bilibili.com: --host must apply to
			// the UGC playurl path too (upstream uses Config.Current.Host here).
			prefix = base + "/x/player/wbi/playurl?"
		}
	}

	if tvAPI {
		var parts []string
		if p.Cfg.Token != "" {
			parts = append(parts, "access_key="+p.Cfg.Token)
		}
		parts = append(parts, "appkey=4409e2ce8ffd12b8", "build=106500", "cid="+cid, "device=android")
		if bangumi {
			parts = append(parts, "ep_id="+epid, "expire=0")
		}
		parts = append(parts,
			"fnval=4048", "fnver=0", "fourk=1", "mid=0", "mobi_app=android_tv_yst",
			"object_id="+aid, "platform=android", "playurl_type=1", "qn="+qn, "ts="+timeStamp(),
		)
		params := strings.Join(parts, "&")
		sign := util.GetSign(params, false)
		api = prefix + params + "&sign=" + sign
	} else {
		var parts []string
		parts = append(parts, "support_multi_audio=true", "from_client=BROWSER", "avid="+aid, "cid="+cid, "fnval=4048", "fnver=0", "fourk=1")
		if p.Cfg.Area != "" {
			parts = append(parts, "access_key="+p.Cfg.Token, "area="+p.Cfg.Area)
		}
		parts = append(parts, "otype=json", "qn="+qn)
		if bangumi {
			parts = append(parts, "module=bangumi", "ep_id="+epid, "session=")
		}
		if p.Cfg.Cookie == "" && !wantDrm {
			parts = append(parts, "try_look=1")
		}
		if wantDrm {
			parts = append(parts, "drm_tech_type=2")
		}
		parts = append(parts, "wts="+timeStamp())
		params := strings.Join(parts, "&")
		if bangumi {
			api = prefix + params
		} else {
			api = prefix + p.WbiSign(params)
		}

		if cheese {
			api = strings.Replace(api, "/pgc/player/web/v2/playurl", "/pugv/player/web/playurl", 1)
		}
	}

	resp, err := p.HTTPClient.GetWebSource(ctx, api)
	if err != nil {
		return "", err
	}

	// Fallback: if response indicates VIP-only content, try parsing from webpage source
	if isVipRestricted(resp) && epid != "" {
		util.Log("此视频需要大会员，您大概率需要登录一个有大会员的账号才可以下载，尝试从网页源码解析")
		// Follow the configured mirror: only the default api host falls back to the
		// official web host, otherwise a mirror user would be pulled back to a
		// domain that may be unreachable for them (upstream EpHost handling).
		webHost := p.Cfg.EpHost
		if webHost == "" || webHost == "api.bilibili.com" {
			webHost = "www.bilibili.com"
		}
		webURL := apiBase(webHost) + "/bangumi/play/ep" + epid
		webSource, err := p.HTTPClient.GetWebSource(ctx, webURL)
		if err != nil {
			return "", fmt.Errorf("大会员回退请求失败: %w", err)
		}
		match := playerJSONRegex.FindStringSubmatch(webSource)
		if match == nil || match[1] == "" {
			return "", fmt.Errorf("大会员回退失败：网页源码中未找到播放信息")
		}
		resp = match[1]
	}

	return resp, nil
}

func (p *Parser) getIntlPlayJSON(ctx context.Context, aid, cid, epid, qn, code string) (string, error) {
	isBiliPlus := p.Cfg.Host != "api.bilibili.com"
	host := p.Cfg.Host
	if !isBiliPlus {
		host = "api.biliintl.com"
	}

	var parts []string
	if p.Cfg.Token != "" {
		parts = append(parts, "access_key="+p.Cfg.Token)
	}
	parts = append(parts, "aid="+aid)
	if isBiliPlus {
		area := p.Cfg.Area
		if area == "" {
			area = "th"
		}
		parts = append(parts, "appkey=7d089525d3611b1c", "area="+area)
	}
	parts = append(parts, "cid="+cid, "ep_id="+epid, "platform=android", "prefer_code_type="+code, "qn="+qn)
	if isBiliPlus {
		parts = append(parts, "ts="+timeStamp())
	}
	parts = append(parts, "s_locale=zh_SG")
	params := strings.Join(parts, "&")
	api := apiBase(host) + "/intl/gateway/v2/ogv/playurl?"
	if isBiliPlus {
		api += params + "&sign=" + util.GetSign(params, true)
	} else {
		api += params
	}

	return p.HTTPClient.GetWebSource(ctx, api)
}

func (p *Parser) parseDomesticStreams(ctx context.Context, result *entity.ParsedResult, aidOri, aid, cid, epid string, tvAPI, appAPI bool, encoding string, wantDrm bool) (*entity.ParsedResult, error) {
	var root map[string]interface{}
	if err := util.UnmarshalJSON(result.WebJSONString, &root); err != nil {
		return nil, fmt.Errorf("parse playurl JSON: %w", err)
	}

	// 播放限制与业务错误码（上游在解析前统一兜底：play_check 只在 pgc 的 result 节点出现，
	// UGC 的限制通过顶层 code 表达）
	if err := throwIfPlayLimited(root); err != nil {
		return nil, err
	}
	if err := throwIfBizError(root); err != nil {
		return nil, err
	}

	// Navigate to data node
	var data map[string]interface{}
	if resultData, ok := root["result"].(map[string]interface{}); ok {
		if vi, ok := resultData["video_info"].(map[string]interface{}); ok {
			data = vi
		} else {
			data = resultData
		}
	} else if d, ok := root["data"].(map[string]interface{}); ok {
		data = d
	} else {
		data = root
	}

	// Extract DRM metadata
	if isDrm, ok := data["is_drm"].(bool); ok {
		result.IsDrm = isDrm
	}
	if drmTech, ok := data["drm_tech_type"].(float64); ok {
		result.DrmTechType = int(drmTech)
	}
	if drmType, ok := data["drm_type"].(string); ok {
		result.DrmType = drmType
	}

	// DASH streams
	if dash, ok := data["dash"].(map[string]interface{}); ok {
		var pDur int
		if dur, ok := dash["duration"].(float64); ok {
			pDur = int(dur)
		} else if tl, ok := data["timelength"].(float64); ok {
			pDur = int(tl) / 1000
		}
		result.ActualDurationSec = pDur

		// 背景音频与配音只在 APP API + 番剧场景出现（上游同一门控）。
		// 缺了这段解析，工作流里的背景音轨/配音下载循环永远拿不到数据。
		if shouldExtractDubbing(appAPI, aidOri) {
			extractDubbingInfo(data, result, aid, cid, pDur)
		}

		// Parse video tracks
		if videos, ok := dash["video"].([]interface{}); ok {
			for _, v := range videos {
				vm, ok := v.(map[string]interface{})
				if !ok {
					continue
				}
				videoID := getString(vm, "id")
				baseURL := getString(vm, "base_url")
				if baseURL == "" {
					continue
				}
				urlList := []string{baseURL}
				if backups, ok := vm["backup_url"].([]interface{}); ok {
					for _, b := range backups {
						if bs, ok := b.(string); ok {
							urlList = append(urlList, bs)
						}
					}
				}
				// Filter out base URL regex matches
				finalURL := urlList[0]
				for _, u := range urlList {
					if !baseURLRegex.MatchString(u) {
						finalURL = u
						break
					}
				}

				video := entity.Video{
					ID:        videoID,
					Dfn:       config.QualityMap[videoID],
					BaseURL:   finalURL,
					Codecs:    VideoCodec(getString(vm, "codecid")),
					Bandwidth: getInt64(vm, "bandwidth") / 1000,
					Dur:       pDur,
					Size:      getFloat64(vm, "size"),
				}
				if video.Dfn == "" {
					video.Dfn = fmt.Sprintf("未知(%s)", videoID)
				}
				if !tvAPI && !appAPI {
					video.Res = fmt.Sprintf("%sx%s", getString(vm, "width"), getString(vm, "height"))
					video.FPS = getString(vm, "frame_rate")
				}
				result.VideoTracks = appendUniqueVideo(result.VideoTracks, video)
			}

			// Extract DRM info from first video
			if result.IsDrm && result.KidHex == "" && len(videos) > 0 {
				if firstV, ok := videos[0].(map[string]interface{}); ok {
					if drmURI, ok := firstV["bilidrm_uri"].(string); ok {
						if kid := parseKidFromDrmURI(drmURI); kid != "" {
							result.KidHex = kid
						}
					}
					if pssh, ok := firstV["widevine_pssh"].(string); ok && pssh != "" {
						result.PsshBase64 = pssh
					}
				}
			}
		}

		// Parse audio tracks
		if audios, ok := dash["audio"].([]interface{}); ok {
			for _, a := range audios {
				am, ok := a.(map[string]interface{})
				if !ok {
					continue
				}
				baseURL := getString(am, "base_url")
				if baseURL == "" {
					continue
				}
				urlList := []string{baseURL}
				if backups, ok := am["backup_url"].([]interface{}); ok {
					for _, b := range backups {
						if bs, ok := b.(string); ok {
							urlList = append(urlList, bs)
						}
					}
				}
				finalURL := urlList[0]
				for _, u := range urlList {
					if !baseURLRegex.MatchString(u) {
						finalURL = u
						break
					}
				}
				audioID := getString(am, "id")
				codecs := getString(am, "codecs")
				switch codecs {
				case "mp4a.40.2", "mp4a.40.5":
					codecs = "M4A"
				case "ec-3":
					codecs = "E-AC-3"
				case "fLaC":
					codecs = "FLAC"
				}

				audio := entity.Audio{
					ID:        audioID,
					Dfn:       audioID,
					BaseURL:   finalURL,
					Codecs:    codecs,
					Bandwidth: getInt64(am, "bandwidth") / 1000,
					Dur:       pDur,
				}
				result.AudioTracks = append(result.AudioTracks, audio)
			}
		}

		// Dolby audio
		if dolby, ok := dash["dolby"].(map[string]interface{}); ok && !tvAPI {
			if dbAudios, ok := dolby["audio"].([]interface{}); ok {
				for _, a := range dbAudios {
					am, _ := a.(map[string]interface{})
					if am == nil {
						continue
					}
					baseURL := getString(am, "base_url")
					if baseURL == "" {
						continue
					}
					audio := entity.Audio{
						ID:        getString(am, "id"),
						Dfn:       getString(am, "id"),
						BaseURL:   baseURL,
						Codecs:    "E-AC-3",
						Bandwidth: getInt64(am, "bandwidth") / 1000,
						Dur:       pDur,
					}
					result.AudioTracks = append(result.AudioTracks, audio)
				}
			}
		}

		// Hi-Res FLAC
		if flac, ok := dash["flac"].(map[string]interface{}); ok && !tvAPI {
			if flacAudio, ok := flac["audio"].(map[string]interface{}); ok {
				baseURL := getString(flacAudio, "base_url")
				if baseURL != "" {
					audio := entity.Audio{
						ID:        getString(flacAudio, "id"),
						Dfn:       getString(flacAudio, "id"),
						BaseURL:   baseURL,
						Codecs:    "FLAC",
						Bandwidth: getInt64(flacAudio, "bandwidth") / 1000,
						Dur:       pDur,
					}
					result.AudioTracks = append(result.AudioTracks, audio)
				}
			}
		}
		return result, nil
	}

	// FLV streams
	if durls, ok := data["durl"].([]interface{}); ok {
		quality := getString(data, "quality")
		videoCodecid := getString(data, "video_codecid")

		var size, length float64
		for _, d := range durls {
			dm, _ := d.(map[string]interface{})
			if dm == nil {
				continue
			}
			if url, ok := dm["url"].(string); ok {
				result.Clips = append(result.Clips, url)
			}
			size += getFloat64(dm, "size")
			length += getFloat64(dm, "length")
		}

		// Available qualities
		if qnExtras, ok := data["qn_extras"].([]interface{}); ok {
			for _, q := range qnExtras {
				if qs, ok := q.(map[string]interface{}); ok {
					if qn, ok := qs["qn"].(string); ok {
						result.Dfns = append(result.Dfns, qn)
					}
				}
			}
		} else if acceptQ, ok := data["accept_quality"].([]interface{}); ok {
			for _, q := range acceptQ {
				if qn, ok := q.(string); ok {
					result.Dfns = append(result.Dfns, qn)
				} else if qn, ok := q.(float64); ok {
					result.Dfns = append(result.Dfns, fmt.Sprintf("%.0f", qn))
				}
			}
		}

		result.ActualDurationSec = int(length) / 1000
		video := entity.Video{
			ID:     quality,
			Dfn:    config.QualityMap[quality],
			Codecs: VideoCodec(videoCodecid),
			Dur:    int(length) / 1000,
			Size:   size,
		}
		result.VideoTracks = appendUniqueVideo(result.VideoTracks, video)
	}

	return result, nil
}

func (p *Parser) parseIntlStreams(ctx context.Context, result *entity.ParsedResult, aid, cid, epid, qn string) (*entity.ParsedResult, error) {
	for _, code := range []string{"0", "1"} {
		if code == "1" {
			var err error
			result.WebJSONString, err = p.getIntlPlayJSON(ctx, aid, cid, epid, qn, code)
			if err != nil {
				continue
			}
		}

		var root map[string]interface{}
		if err := util.UnmarshalJSON(result.WebJSONString, &root); err != nil {
			continue
		}

		data, ok := root["data"].(map[string]interface{})
		if !ok {
			continue
		}
		videoInfo, ok := data["video_info"].(map[string]interface{})
		if !ok {
			continue
		}
		streamList, ok := videoInfo["stream_list"].([]interface{})
		if !ok {
			continue
		}

		pDur := int(getFloat64(videoInfo, "timelength") / 1000)
		var audioElements []map[string]interface{}
		if dashAudios, ok := videoInfo["dash_audio"].([]interface{}); ok {
			for _, a := range dashAudios {
				if am, ok := a.(map[string]interface{}); ok {
					audioElements = append(audioElements, am)
				}
			}
		}

		for _, stream := range streamList {
			sm, ok := stream.(map[string]interface{})
			if !ok {
				continue
			}
			dashVideo, ok := sm["dash_video"].(map[string]interface{})
			if !ok {
				continue
			}
			baseURL := getString(dashVideo, "base_url")
			if baseURL == "" {
				continue
			}

			streamInfo, ok := sm["stream_info"].(map[string]interface{})
			if !ok {
				continue
			}

			videoID := getString(streamInfo, "quality")
			urlList := []string{baseURL}
			if backups, ok := dashVideo["backup_url"].([]interface{}); ok {
				for _, b := range backups {
					if bs, ok := b.(string); ok {
						urlList = append(urlList, bs)
					}
				}
			}
			finalURL := urlList[0]
			for _, u := range urlList {
				if !baseURLRegex.MatchString(u) {
					finalURL = u
					break
				}
			}

			v := entity.Video{
				ID:        videoID,
				Dfn:       config.QualityMap[videoID],
				BaseURL:   finalURL,
				Codecs:    VideoCodec(getString(dashVideo, "codecid")),
				Bandwidth: getInt64(dashVideo, "bandwidth") / 1000,
				Dur:       pDur,
				Size:      getFloat64(dashVideo, "size"),
			}
			if v.Dfn == "" {
				v.Dfn = fmt.Sprintf("未知(%s)", videoID)
			}
			result.VideoTracks = appendUniqueVideo(result.VideoTracks, v)
		}

		for _, node := range audioElements {
			baseURL := getString(node, "base_url")
			if baseURL == "" {
				continue
			}
			urlList := []string{baseURL}
			if backups, ok := node["backup_url"].([]interface{}); ok {
				for _, b := range backups {
					if bs, ok := b.(string); ok {
						urlList = append(urlList, bs)
					}
				}
			}
			finalURL := urlList[0]
			for _, u := range urlList {
				if !baseURLRegex.MatchString(u) {
					finalURL = u
					break
				}
			}

			audio := entity.Audio{
				ID:        getString(node, "id"),
				Dfn:       getString(node, "id"),
				BaseURL:   finalURL,
				Codecs:    "M4A",
				Bandwidth: getInt64(node, "bandwidth") / 1000,
				Dur:       pDur,
			}
			result.AudioTracks = appendUniqueAudio(result.AudioTracks, audio)
		}
	}

	return result, nil
}

// ---- Helpers ----

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case float64:
			if val == float64(int64(val)) {
				return strconv.FormatInt(int64(val), 10)
			}
			return strconv.FormatFloat(val, 'f', -1, 64)
		case json.Number:
			return string(val)
		}
	}
	return ""
}

func getInt64(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return int64(val)
		case json.Number:
			n, _ := val.Int64()
			return n
		case string:
			n, _ := strconv.ParseInt(val, 10, 64)
			return n
		}
	}
	return 0
}

func getFloat64(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case json.Number:
			f, _ := val.Float64()
			return f
		}
	}
	return 0
}

func appendUniqueVideo(videos []entity.Video, v entity.Video) []entity.Video {
	for _, existing := range videos {
		if existing.ID == v.ID && existing.Dfn == v.Dfn && existing.Res == v.Res && existing.FPS == v.FPS && existing.Codecs == v.Codecs && existing.Bandwidth == v.Bandwidth && existing.Dur == v.Dur {
			return videos
		}
	}
	return append(videos, v)
}

func appendUniqueAudio(audios []entity.Audio, a entity.Audio) []entity.Audio {
	for _, existing := range audios {
		if existing.ID == a.ID && existing.Dfn == a.Dfn && existing.Codecs == a.Codecs && existing.Bandwidth == a.Bandwidth && existing.Dur == a.Dur {
			return audios
		}
	}
	return append(audios, a)
}
