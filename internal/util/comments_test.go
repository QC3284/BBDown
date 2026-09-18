package util

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSaveCommentsJSONCreatesParentDir: the export path is template-derived and
// may point into a directory that the download never created.
func TestSaveCommentsJSONCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aid123", "nested", "video.comments.json")

	items := []CommentItem{{User: "tester", Content: "hi"}}
	if err := SaveCommentsJSON(items, path); err != nil {
		t.Fatalf("SaveCommentsJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("comment file missing: %v", err)
	}
	var back []CommentItem
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
	if len(back) != 1 || back[0].Content != "hi" || back[0].User != "tester" {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}
