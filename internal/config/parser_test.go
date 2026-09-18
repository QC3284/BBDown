package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMergeWithConfig(t *testing.T) {
	aliasMap := map[string]string{"--dfn-priority": "dfn", "-q": "dfn", "--cookie": "cookie", "-c": "cookie", "--multi-thread": "multi"}
	boolFlags := map[string]bool{"multi": true, "cookie": false, "dfn": false}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "BBDown.config")
	lines := []string{
		"# comment",
		"BV1xx411c7mD", // config URL (honored when the CLI has no URL)
		"--cookie abc123",
		"--dfn-priority 8K,1080P",
	}
	if err := os.WriteFile(cfgPath, []byte(joinLines(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	// CLI has a URL: config URL dropped; explicit --cookie overrides config.
	merged, err := MergeWithConfig([]string{"--config-file", cfgPath, "--cookie", "cli-cookie", "av170001"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--config-file", cfgPath, "--cookie", "cli-cookie", "av170001", "--dfn-priority", "8K,1080P"}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
}

func TestUrlLikeToken(t *testing.T) {
	if !urlLikeToken.MatchString("av170001") {
		t.Fatal("av170001 should match urlLikeToken")
	}
	if !urlLikeToken.MatchString("BV1xx411c7mD") {
		t.Fatal("BV should match urlLikeToken")
	}
	if urlLikeToken.MatchString("not-a-url") {
		t.Fatal("plain text should not match")
	}
}

func TestIsSubCommandInvocation(t *testing.T) {
	aliasMap := map[string]string{"--cookie": "cookie", "-c": "cookie", "--limit": "limit", "--config-file": "configfile"}
	boolFlags := map[string]bool{"limit": true, "cookie": false}
	if !IsSubCommandInvocation([]string{"watchlater", "--limit", "5"}, aliasMap, boolFlags) {
		t.Fatal("watchlater should be a subcommand invocation")
	}
	if !IsSubCommandInvocation([]string{"--config-file", "x.config", "sub", "list"}, aliasMap, boolFlags) {
		t.Fatal("sub after value-consuming option should be detected")
	}
	if IsSubCommandInvocation([]string{"av170001"}, aliasMap, boolFlags) {
		t.Fatal("plain download should not be a subcommand invocation")
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func TestMergeWithConfigKeepsURLWhenOptionValueLooksLikeURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "BBDown.config")
	if err := os.WriteFile(cfgPath, []byte("--encoding-priority\nhevc,avc\n# 目标\nhttps://www.bilibili.com/video/BV1xx411c7mD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	aliasMap := map[string]string{
		"--config-file":  "config-file",
		"--aria2c-proxy": "aria2c-proxy",
		"--work-dir":     "work-dir",
		"--audio-only":   "audio-only",
	}
	boolFlags := map[string]bool{"audio-only": true}

	// Both option values look like a target (a proxy URL and "av123"), but both
	// are consumed by their option, so the config URL must survive (upstream RF-7).
	args := []string{"--config-file", cfgPath, "--aria2c-proxy", "http://127.0.0.1:1080", "--work-dir", "av123"}
	merged, err := MergeWithConfig(args, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(merged, " "), "BV1xx411c7mD") {
		t.Errorf("config URL was dropped because an option value looked like a URL: %v", merged)
	}

	// A real CLI target must still suppress the config one.
	merged, err = MergeWithConfig([]string{"--config-file", cfgPath, "BV1yy411c7mD", "--audio-only"}, aliasMap, boolFlags)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(merged, " "), "BV1xx411c7mD") {
		t.Errorf("config URL should be dropped when the CLI already carries one: %v", merged)
	}
}
