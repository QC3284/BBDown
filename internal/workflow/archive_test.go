package workflow

import "testing"

// Ported from upstream ArchiveGranularityTests: archiving is per aid, but a
// multi-page video shares one aid, so the record must wait for the last page.
// The previous code archived after the first page, which made the remaining
// pages of the same video skip as "already downloaded" on the next run.

func TestMultiPageVideoArchivedOnlyAfterEveryPageSucceeds(t *testing.T) {
	tr := NewArchiveTracker([]string{"100", "100", "100"})
	if tr.OnProcessed("100", true) {
		t.Error("must not archive after the first page")
	}
	if tr.OnProcessed("100", true) {
		t.Error("must not archive after the second page")
	}
	if !tr.OnProcessed("100", true) {
		t.Error("must archive once every page succeeded")
	}
}

func TestMultiPageVideoNotArchivedWhenAnyPageFails(t *testing.T) {
	tr := NewArchiveTracker([]string{"100", "100", "100"})
	if tr.OnProcessed("100", true) {
		t.Error("must not archive after the first page")
	}
	if tr.OnProcessed("100", false) {
		t.Error("a failed page must disqualify the aid")
	}
	if tr.OnProcessed("100", true) {
		t.Error("an earlier failure must still block archiving at the end")
	}
}

func TestFailureOnLastPageAlsoPreventsArchiving(t *testing.T) {
	tr := NewArchiveTracker([]string{"100", "100"})
	if tr.OnProcessed("100", true) {
		t.Error("must not archive after the first page")
	}
	if tr.OnProcessed("100", false) {
		t.Error("a failure on the last page must block archiving")
	}
}

func TestSinglePageVideoIsArchivedImmediately(t *testing.T) {
	tr := NewArchiveTracker([]string{"100"})
	if !tr.OnProcessed("100", true) {
		t.Error("a single-page video must archive right away")
	}
}

// TestArchiveTrackerSeparatesAids: distinct aids (e.g. bangumi episodes) archive
// independently.
func TestArchiveTrackerSeparatesAids(t *testing.T) {
	tr := NewArchiveTracker([]string{"1", "2"})
	if !tr.OnProcessed("2", true) {
		t.Error("aid 2 is complete and must archive")
	}
	if !tr.OnProcessed("1", true) {
		t.Error("aid 1 is complete and must archive")
	}
}
