package substore

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempRoot switches the store root to a temp dir for the test.
func withTempRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := StoreRoot
	StoreRoot = dir
	t.Cleanup(func() { StoreRoot = old })
	return dir
}

func TestSubStoreAddListRemove(t *testing.T) {
	withTempRoot(t)

	if err := Add("mid:123", "某人"); err != nil {
		t.Fatal(err)
	}
	if err := Add("ep:456", ""); err != nil {
		t.Fatal(err)
	}

	subs, err := ListSorted()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("want 2 subs, got %d", len(subs))
	}
	// Unnamed subscription falls back to the target string.
	if subs[1].Target != "ep:456" || subs[1].Name != "ep:456" {
		t.Fatalf("fallback name wrong: %+v", subs[1])
	}

	if err := Remove("mid:123"); err != nil {
		t.Fatal(err)
	}
	subs, err = ListSorted()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Target != "ep:456" {
		t.Fatalf("remove failed: %+v", subs)
	}
}

func TestSubStoreHistory(t *testing.T) {
	withTempRoot(t)

	history, err := LoadHistory("mid:123")
	if err != nil || history != nil {
		t.Fatalf("empty history = %v, %v", history, err)
	}

	if err := RecordDownloaded("mid:123", "170001"); err != nil {
		t.Fatal(err)
	}
	if err := RecordDownloaded("mid:123", "170001"); err != nil {
		t.Fatal(err)
	}
	history, err = LoadHistory("mid:123")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0] != "170001" {
		t.Fatalf("history = %v", history)
	}
}

func TestSubStoreCorruptDetection(t *testing.T) {
	dir := withTempRoot(t)
	if err := os.WriteFile(filepath.Join(dir, "BBDownSubscriptions.json"), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil {
		t.Fatal("corrupt store should error")
	}
	// The corrupt file must be quarantined, not overwritten.
	matches, _ := filepath.Glob(filepath.Join(dir, "BBDownSubscriptions.json.corrupt-*"))
	if len(matches) != 1 {
		t.Fatalf("corrupt file not quarantined: %v", matches)
	}
}
