package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleXML = `<?xml version="1.0" encoding="UTF-8"?>
<i>
  <chatserver>chat.bilibili.com</chatserver>
  <chatid>279786</chatid>
  <d p="1.5,1,25,16777215,1400000000,0,abc123,0">hello world</d>
  <d p="3.0,4,25,16711680,1400000001,0,def456,0">bottom danmaku</d>
  <d p="5.5,5,25,255,1400000002,0,abc123,0">top danmaku</d>
  <d p="broken">malformed</d>
</i>`

func TestParseDanmakuXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "danmaku.xml")
	if err := os.WriteFile(path, []byte(sampleXML), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := ParseDanmakuXML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("parsed %d items, want 3", len(items))
	}
	if items[0].Content != "hello world" {
		t.Errorf("first content = %q", items[0].Content)
	}
	if items[0].MidHash != "abc123" {
		t.Errorf("first midHash = %q", items[0].MidHash)
	}
	if items[0].Second != 1.5 {
		t.Errorf("first second = %v", items[0].Second)
	}
	if items[1].DanmakuMode != posBottom {
		t.Errorf("second mode = %d, want posBottom", items[1].DanmakuMode)
	}
	if items[2].DanmakuMode != posTop {
		t.Errorf("third mode = %d, want posTop", items[2].DanmakuMode)
	}
	if items[1].Color != "FF0000" {
		t.Errorf("second color = %q, want FF0000", items[1].Color)
	}
}

func TestFilterDanmaku(t *testing.T) {
	items := DanmakuList{
		{Content: "ad ad ad", MidHash: "spam01"},
		{Content: "正常弹幕", MidHash: "user01"},
		{Content: "another normal", MidHash: "user02"},
	}
	got := FilterDanmaku(items, "ad", "")
	if len(got) != 2 || got[0].Content != "正常弹幕" {
		t.Fatalf("keyword filter: %+v", got)
	}
	got = FilterDanmaku(items, "", "spam01")
	if len(got) != 2 {
		t.Fatalf("user filter: %+v", got)
	}
	got = FilterDanmaku(items, "ad, another", "")
	if len(got) != 1 || got[0].Content != "正常弹幕" {
		t.Fatalf("multi keyword filter: %+v", got)
	}
	got = FilterDanmaku(items, "", "")
	if len(got) != 3 {
		t.Fatalf("empty filter should pass through: %+v", got)
	}
}

func TestEscapeAssText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"{not an override}", "｛not an override｝"},
		{"line1\r\nline2", "line1\\Nline2"},
		{"line1\nline2", "line1\\Nline2"},
	}
	for _, c := range cases {
		if got := escapeAssText(c.in); got != c.want {
			t.Errorf("escapeAssText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSaveDanmakuAsASS(t *testing.T) {
	items := DanmakuList{
		{Second: 1, Content: "first", DanmakuMode: posMove, Color: "FFFFFF", StartTime: "0:00:01.00", EndTime: "0:00:09.00"},
		{Second: 2, Content: "second", DanmakuMode: posTop, Color: "FF0000", StartTime: "0:00:02.00", EndTime: "0:00:06.00"},
	}
	out := filepath.Join(t.TempDir(), "out.ass")
	if err := SaveDanmakuAsASS(items, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "[Script Info]") || !strings.Contains(text, "[Events]") {
		t.Fatalf("missing ASS sections:\n%s", text)
	}
	if !strings.Contains(text, "Dialogue: 2,0:00:01.00,0:00:09.00") {
		t.Fatalf("missing first dialogue line:\n%s", text)
	}
	if strings.Count(text, "Dialogue:") != 2 {
		t.Fatalf("expected 2 dialogue lines:\n%s", text)
	}
}
