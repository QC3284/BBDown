package download

import (
	"strings"
	"testing"
)

// TestBuildAria2cInputFileRejectsLineInjection: aria2c reads one option per line,
// so a newline inside an interpolated value appends attacker-chosen options.
// The URL is the server-controlled value here (base_url from the API response).
func TestBuildAria2cInputFileRejectsLineInjection(t *testing.T) {
	got := buildAria2cInputFile(
		"https://upos.example.com/a.m4s\n  out=/etc/passwd",
		false,
		"SESSDATA=x\ndir=/",
		"/tmp/work",
		"a.m4s",
	)

	// Count option *lines*, not substrings: the sanitized URL still contains the
	// injected text on its own line, which aria2c reads as part of the URL.
	countOption := func(key string) int {
		n := 0
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), key+"=") {
				n++
			}
		}
		return n
	}
	if n := countOption("out"); n != 1 {
		t.Errorf("aria2c option injection: %d out= option lines\n%s", n, got)
	}
	if n := countOption("dir"); n != 1 {
		t.Errorf("aria2c option injection: %d dir= option lines\n%s", n, got)
	}
	// url + UA + Cookie + dir + out (no Referer header for this host).
	if n := strings.Count(got, "\n"); n != 5 {
		t.Errorf("expected 5 lines, got %d:\n%s", n, got)
	}
	if !strings.HasSuffix(got, "  out=a.m4s\n") {
		t.Errorf("the real out= must be the last line:\n%s", got)
	}
	if !strings.HasPrefix(got, "https://upos.example.com/a.m4s") {
		t.Errorf("the URL line was mangled:\n%s", got)
	}
}

// TestBuildAria2cInputFileShape pins the normal rendering.
func TestBuildAria2cInputFileShape(t *testing.T) {
	got := buildAria2cInputFile("https://upos.example.com/a.m4s", true, "", "/tmp/work", "a.m4s")
	want := "https://upos.example.com/a.m4s\n" +
		"  header=Referer: https://www.bilibili.com\n" +
		"  header=User-Agent: Mozilla/5.0\n" +
		"  dir=/tmp/work\n" +
		"  out=a.m4s\n"
	if got != want {
		t.Errorf("input file =\n%q\nwant\n%q", got, want)
	}
}
