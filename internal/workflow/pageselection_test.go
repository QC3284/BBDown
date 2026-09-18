package workflow

import (
	"strings"
	"testing"
)

// TestParsePageSelectionCumulativeCap: the cap used to apply per range, so
// "1-60000,1-60000" passed it twice and expanded to 120000 entries — a
// serve-side amplification anyone with /add-task access could trigger.
func TestParsePageSelectionCumulativeCap(t *testing.T) {
	if _, err := parsePageSelection("1-60000,1-60000"); err == nil {
		t.Error("cumulative expansion beyond the cap must be rejected")
	}
	if _, err := parsePageSelection("1-100001"); err == nil {
		t.Error("a single oversized range must be rejected")
	}

	got, err := parsePageSelection("1-3,5,7")
	if err != nil {
		t.Fatalf("a normal selection must still parse: %v", err)
	}
	if strings.Join(got, ",") != "1,2,3,5,7" {
		t.Errorf("selection = %v", got)
	}
}
