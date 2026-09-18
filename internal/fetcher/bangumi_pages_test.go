package fetcher

import "testing"

func TestBuildBangumiPagesSkipsPreviewsAndLocatesEpisode(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{"badge": "预告", "id": "1", "title": "PV"},
		map[string]interface{}{"id": "100", "title": "第一话", "duration": 100},
		map[string]interface{}{"id": "200", "title": "第二话", "long_title": "副标题", "duration": 200},
	}

	// Previews never become downloadable pages, and indices stay dense (1..n).
	pages, index, err := buildBangumiPages(raw, "")
	if err != nil {
		t.Fatalf("requesting the whole season must not error: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %d, want 2 (the 预告 preview must be dropped)", len(pages))
	}
	if pages[0].Index != 1 || pages[1].Index != 2 {
		t.Errorf("indices = %d,%d, want 1,2", pages[0].Index, pages[1].Index)
	}
	if pages[1].Title != "第二话 副标题" {
		t.Errorf("title = %q, want long_title appended", pages[1].Title)
	}
	if index != "" {
		t.Errorf("index = %q, want empty when no episode was requested", index)
	}

	// Locating a requested episode yields its 1-based position.
	if _, index, err := buildBangumiPages(raw, "200"); err != nil || index != "2" {
		t.Errorf("index = %q, err = %v; want \"2\" and no error", index, err)
	}

	// The regression: an unmatched request used to return an empty Index, which
	// made the workflow fall back to ALL pages — an entire season, silently.
	if _, _, err := buildBangumiPages(raw, "999"); err == nil {
		t.Error("an unmatched requested episode must error instead of silently downloading the season")
	}
}
