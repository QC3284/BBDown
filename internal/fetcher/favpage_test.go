package fetcher

import (
	"strings"
	"testing"
)

// TestFavPageActionFor pins the paging contract: only an empty page ends the
// listing; a non-zero code (risk control, error page) must fail loudly instead
// of silently truncating the favourites.
func TestFavPageActionFor(t *testing.T) {
	medias := []interface{}{map[string]interface{}{"id": 1}}

	action, err := favPageActionFor(0, "0", medias)
	if err != nil || action != favContinue {
		t.Errorf("normal page: action=%v err=%v, want continue", action, err)
	}

	action, err = favPageActionFor(0, "0", nil)
	if err != nil || action != favStop {
		t.Errorf("empty page: action=%v err=%v, want a clean stop", action, err)
	}

	_, err = favPageActionFor(-412, "请求被拦截", medias)
	if err == nil {
		t.Fatal("a risk-control page must be reported, not silently truncate the list")
	}
	if !strings.Contains(err.Error(), "-412") {
		t.Errorf("the error should carry the API code, got %v", err)
	}
}
