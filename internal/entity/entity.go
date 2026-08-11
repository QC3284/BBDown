package entity

import (
	"fmt"
	"strconv"
	"strings"
)

// Page represents a video page/part.
type Page struct {
	Index     int    `json:"index"`
	Aid       string `json:"aid"`
	Cid       string `json:"cid"`
	Epid      string `json:"epid"`
	Title     string `json:"title"`
	Dur       int    `json:"dur"`
	Res       string `json:"res"`
	PubTime   int64  `json:"pub_time"`
	Cover     string `json:"cover,omitempty"`
	Desc      string `json:"desc,omitempty"`
	OwnerName string `json:"owner_name,omitempty"`
	OwnerMid  string `json:"owner_mid,omitempty"`
	Points    []ViewPoint `json:"points,omitempty"`
}

// Bvid returns the BV string for this page's aid.
func (p Page) Bvid() string {
	if aid, err := strconv.ParseInt(p.Aid, 10, 64); err == nil {
		if bv, err := avToBv(aid); err == nil {
			return bv
		}
	}
	return p.Aid
}

// ---- BV ↔ AV conversion (inline to avoid circular import) ----

const (
	bvXor  int64 = 23442827791579
	bvMask int64 = (1 << 51) - 1
	bvBase int64 = 58
	bvLen2      = 9
)

var bvAlphabet = []byte("FcwAPNKTMug3GV5Lj7EJnHpWsx4tb8haYeviqBz6rkCy12mUSDQX9RdoZf")
var bvRev = make(map[byte]int64)

func init() {
	for i, b := range bvAlphabet {
		bvRev[b] = int64(i)
	}
}

func avToBv(aid int64) (string, error) {
	if aid < 1 || aid >= bvMask+1 {
		return "", fmt.Errorf("av out of range")
	}
	bvid := make([]byte, bvLen2)
	tmp := (bvMask + 1 | aid) ^ bvXor
	for i := bvLen2 - 1; i >= 0 && tmp != 0; i-- {
		bvid[i] = bvAlphabet[tmp%bvBase]
		tmp /= bvBase
	}
	bvid[0], bvid[6] = bvid[6], bvid[0]
	bvid[1], bvid[4] = bvid[4], bvid[1]
	return "BV1" + string(bvid), nil
}

// BvToAv converts a BV suffix to aid.
func BvToAv(bvSuffix string) (int64, error) {
	if len(bvSuffix) != bvLen2 {
		return 0, fmt.Errorf("BV suffix must be %d chars", bvLen2)
	}
	bvid := []byte(bvSuffix)
	bvid[0], bvid[6] = bvid[6], bvid[0]
	bvid[1], bvid[4] = bvid[4], bvid[1]
	var aid int64
	for _, b := range bvid {
		idx, ok := bvRev[b]
		if !ok {
			return 0, fmt.Errorf("invalid BV char: %c", b)
		}
		aid = aid*bvBase + idx
	}
	return (aid & bvMask) ^ bvXor, nil
}

// AvToBvStr converts aid string to BV, or returns as-is.
func AvToBvStr(aidStr string) string {
	if aid, err := strconv.ParseInt(aidStr, 10, 64); err == nil {
		if bv, err := avToBv(aid); err == nil {
			return bv
		}
	}
	return aidStr
}

// Ensure unused import is used.
var _ = strings.TrimSpace

// ViewPoint represents a chapter/section marker.
type ViewPoint struct {
	Title string `json:"title"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Video represents a video track.
type Video struct {
	ID        string `json:"id"`
	Dfn       string `json:"dfn"` // quality display name
	BaseURL   string `json:"base_url"`
	Res       string `json:"res,omitempty"`
	FPS       string `json:"fps,omitempty"`
	Codecs    string `json:"codecs"`
	Bandwidth int64  `json:"bandwidth"`
	Dur       int    `json:"dur"`
	Size      float64 `json:"size"`
}

// Audio represents an audio track.
type Audio struct {
	ID        string `json:"id"`
	Dfn       string `json:"dfn"`
	BaseURL   string `json:"base_url"`
	Codecs    string `json:"codecs"`
	Bandwidth int64  `json:"bandwidth"`
	Dur       int    `json:"dur"`
}

// ShortCodecs returns the short codec name (E-AC-3 => EAC3).
func (a Audio) ShortCodecs() string {
	s := a.Codecs
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			s = s[:i] + string(s[i]-32) + s[i+1:]
		}
	}
	// remove dashes
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			result = append(result, s[i])
		}
	}
	return string(result)
}

// Subtitle represents a subtitle track.
type Subtitle struct {
	Lan  string `json:"lan"`
	URL  string `json:"url"`
	Path string `json:"path"`
}

// Clip represents a media segment.
type Clip struct {
	Index int   `json:"index"`
	From  int64 `json:"from"`
	To    int64 `json:"to"`
}

// AudioMaterial represents an audio material track.
type AudioMaterial struct {
	Title      string `json:"title"`
	PersonName string `json:"person_name"`
	Path       string `json:"path"`
}

// AudioMaterialInfo represents audio material with track list.
type AudioMaterialInfo struct {
	Title      string  `json:"title"`
	PersonName string  `json:"person_name"`
	Path       string  `json:"path"`
	Audio      []Audio `json:"audio"`
}
