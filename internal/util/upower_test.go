package util

import "testing"

func TestInspectUpower(t *testing.T) {
	// Exclusive without play permission: preview regardless of duration.
	v := InspectUpower(true, false, 3600, 3600)
	if !v.IsPreview {
		t.Fatal("exclusive without play permission should be preview")
	}

	// Duration mismatch beyond thresholds: preview.
	v = InspectUpower(false, true, 3600, 3000)
	if !v.IsPreview {
		t.Fatal("large duration mismatch should be preview")
	}

	// Gap below minimum absolute threshold: not preview.
	v = InspectUpower(false, true, 3600, 3580)
	if v.IsPreview {
		t.Fatal("small gap should not be preview")
	}

	// Ratio above 0.9: not preview.
	v = InspectUpower(false, true, 100, 95)
	if v.IsPreview {
		t.Fatal("gap above ratio threshold should not be preview")
	}

	// Normal case.
	v = InspectUpower(false, true, 3600, 3600)
	if v.IsPreview {
		t.Fatal("normal video should not be preview")
	}

	// Zero durations: no mismatch detection.
	v = InspectUpower(false, true, 0, 0)
	if v.IsPreview {
		t.Fatal("zero durations should not be preview")
	}
}
