package substore

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// TestRecordDownloadedKeepsMostRecentAndCapsHistory: the history is ordered by
// recency, an already-known aid moves to the end, and the oldest entries are
// evicted once the cap is reached — otherwise it grows forever and every
// `sub check` parses it in full.
func TestRecordDownloadedKeepsMostRecentAndCapsHistory(t *testing.T) {
	dir := t.TempDir()
	oldRoot := StoreRoot
	StoreRoot = dir
	defer func() { StoreRoot = oldRoot }()

	if err := RecordDownloaded("mid:1", "av1"); err != nil {
		t.Fatal(err)
	}
	if err := RecordDownloaded("mid:1", "av2"); err != nil {
		t.Fatal(err)
	}
	// Re-recording av1 must move it to the end, not be a no-op.
	if err := RecordDownloaded("mid:1", "av1"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadHistory("mid:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "av2" || got[1] != "av1" {
		t.Fatalf("history = %v, want [av2 av1] with av1 most recent", got)
	}

	// Seed a full history, then add one more: only the oldest may be evicted.
	seed := make([]string, maxHistoryPerTarget)
	for i := range seed {
		seed[i] = "av" + strconv.Itoa(i)
	}
	data, err := json.Marshal(map[string][]string{"mid:2": seed})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyFile(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordDownloaded("mid:2", "avNEW"); err != nil {
		t.Fatal(err)
	}
	capped, err := LoadHistory("mid:2")
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != maxHistoryPerTarget {
		t.Fatalf("history length = %d, want %d", len(capped), maxHistoryPerTarget)
	}
	if capped[len(capped)-1] != "avNEW" {
		t.Errorf("the newest entry must survive, got %q", capped[len(capped)-1])
	}
	if capped[0] != "av1" {
		t.Errorf("only the oldest entry may be evicted, got %q at the front", capped[0])
	}
}
