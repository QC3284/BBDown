package download

import (
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/entity"
)

func TestFormatSavePath(t *testing.T) {
	page := entity.Page{
		Index:     3,
		Aid:       "170001",
		Cid:       "279786",
		Title:     "第三话",
		OwnerName: "UP主",
		OwnerMid:  "12345",
		PubTime:   1500000000,
	}
	video := &entity.Video{ID: "80", Dfn: "1080P 高清", Codecs: "AVC", Res: "1920x1080", FPS: "30", Bandwidth: 3000}
	audio := &entity.Audio{ID: "30280", Codecs: "M4A", Bandwidth: 192}

	got := FormatSavePath("<videoTitle>/[P<pageNumberWithZero>]<pageTitle>", "标题", video, audio, page, 12, "WEB", 1500000000)
	want := "标题/[P03]第三话"
	if got != want {
		t.Errorf("FormatSavePath = %q, want %q", got, want)
	}

	dateStr := time.Unix(1500000000, 0).Local().Format("2006-01-02_15-04-05")
	got = FormatSavePath("<bvid>_<ownerName>_<ownerMid>_<dfn>_<videoCodecs>_<audioCodecs>_<videoBandwidth>_<audioBandwidth>_<publishDate>", "t", video, audio, page, 1, "WEB", 1500000000)
	want = "BV17x411w7KC_UP主_12345_1080P 高清_AVC_M4A_3000_192_" + dateStr
	if got != want {
		t.Errorf("FormatSavePath(placeholders) = %q, want %q", got, want)
	}
}

func TestFormatSavePathPageNumberPadding(t *testing.T) {
	page := entity.Page{Index: 7}
	got := FormatSavePath("<pageNumberWithZero>", "t", nil, nil, page, 100, "WEB", 0)
	if got != "007" {
		t.Errorf("padding = %q, want 007", got)
	}
	got = FormatSavePath("<pageNumberWithZero>", "t", nil, nil, page, 9, "WEB", 0)
	if got != "7" {
		t.Errorf("padding = %q, want 7", got)
	}
}

func TestSortVideoTracksPriority(t *testing.T) {
	tracks := []entity.Video{
		{ID: "1", Dfn: "1080P 高清", Codecs: "HEVC", Bandwidth: 3000},
		{ID: "2", Dfn: "8K 超高清", Codecs: "AVC", Bandwidth: 8000},
		{ID: "3", Dfn: "1080P 高清", Codecs: "AVC", Bandwidth: 2000},
	}
	// Priority: HEVC first; among AVC, 1080P(0) outranks 8K(1).
	encoding := map[string]int{"HEVC": 0, "AVC": 1}
	dfn := map[string]int{"1080P 高清": 0, "8K 超高清": 1}
	sorted := SortVideoTracks(tracks, dfn, encoding, false)
	if sorted[0].ID != "1" || sorted[1].ID != "3" || sorted[2].ID != "2" {
		t.Errorf("sorted ids = %s,%s,%s", sorted[0].ID, sorted[1].ID, sorted[2].ID)
	}

	// Same priorities: id descending as tiebreak.
	tracks2 := []entity.Video{
		{ID: "16", Dfn: "360P 流畅", Codecs: "AVC", Bandwidth: 300},
		{ID: "32", Dfn: "480P 清晰", Codecs: "AVC", Bandwidth: 300},
	}
	sorted2 := SortVideoTracks(tracks2, nil, nil, false)
	if sorted2[0].ID != "32" {
		t.Errorf("id desc tiebreak failed: first = %s", sorted2[0].ID)
	}
}

func TestSortAudioTracksShortCodecs(t *testing.T) {
	tracks := []entity.Audio{
		{ID: "1", Codecs: "E-AC-3", Bandwidth: 448},
		{ID: "2", Codecs: "M4A", Bandwidth: 192},
	}
	encoding := map[string]int{"EAC3": 0, "M4A": 1}
	sorted := SortAudioTracks(tracks, encoding, false)
	if sorted[0].ID != "1" {
		t.Errorf("first = %s, want E-AC-3 track", sorted[0].ID)
	}
}
