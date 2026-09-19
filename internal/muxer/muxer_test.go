package muxer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/entity"
)

// TestFFmpegArgsKeepOutputOptionsAfterAllInputs pins the v1.6.16 alignment fix:
// -metadata/-disposition/-map_chapters are OUTPUT options and must be emitted
// after every -i. ffmpeg applies an option to the next file, so emitting them
// between two -i arguments turns them into input options and the mux dies with
// "cannot be applied to input url" (exit 234).
func TestFFmpegArgsKeepOutputOptionsAfterAllInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ffmpeg is a POSIX shell script")
	}

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "fake-ffmpeg")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	origFFmpeg := FFMPEG
	FFMPEG = script
	defer func() { FFMPEG = origFFmpeg }()

	sub := filepath.Join(dir, "zh.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := MuxAV(context.Background(), false, "BV1xx411c7mD",
		filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.m4a"), filepath.Join(dir, "out.mp4"),
		"desc", "title", "author", "ep1", filepath.Join(dir, "cover.jpg"), "zh-CN",
		[]entity.Subtitle{{Lan: "zh-CN", Path: sub}},
		false, false, false,
		[]entity.ViewPoint{{Title: "开场", Start: 0, End: 1000}, {Title: "正片", Start: 1000, End: 5000}},
		1700000000, false, nil, 5)
	if err != nil {
		t.Fatalf("MuxAV returned error: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	lastInput := -1
	for i, a := range args {
		if a == "-i" {
			lastInput = i
		}
	}
	if lastInput < 0 {
		t.Fatalf("no -i argument produced: %v", args)
	}

	seen := map[string]bool{}
	for i, a := range args {
		isOutputOpt := strings.HasPrefix(a, "-metadata:s:") || strings.HasPrefix(a, "-disposition") || a == "-map_chapters"
		if !isOutputOpt {
			continue
		}
		seen[a] = true
		if i < lastInput {
			t.Errorf("output option %q at index %d precedes the last -i at index %d\nargs: %v", a, i, lastInput, args)
		}
	}

	// Guard against "fixing" the order by silently dropping the options.
	for _, want := range []string{"-metadata:s:s:0", "-disposition:v:1", "-map_chapters"} {
		if !seen[want] {
			t.Errorf("expected option %q to still be emitted\nargs: %v", want, args)
		}
	}
}

// TestMuxAVEndToEndCoverChaptersAndSubtitle is the real-media counterpart: it
// exercises the exact combination that used to fail (cover + chapters + a
// subtitle track) against a real ffmpeg. Skipped when ffmpeg is unavailable.
func TestMuxAVEndToEndCoverChaptersAndSubtitle(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	dir := t.TempDir()
	video := filepath.Join(dir, "v.mp4")
	audio := filepath.Join(dir, "a.m4a")
	cover := filepath.Join(dir, "cover.jpg")
	sub := filepath.Join(dir, "zh.srt")
	out := filepath.Join(dir, "out.mp4")

	gen := func(what string, args ...string) {
		full := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)
		if b, err := exec.Command(ffmpeg, full...).CombinedOutput(); err != nil {
			t.Skipf("cannot build %s fixture: %v\n%s", what, err, b)
		}
	}
	gen("video", "-f", "lavfi", "-i", "testsrc=d=1:s=64x48:r=10", "-c:v", "libx264", "-pix_fmt", "yuv420p", video)
	gen("audio", "-f", "lavfi", "-i", "sine=d=1", "-c:a", "aac", audio)
	gen("cover", "-f", "lavfi", "-i", "testsrc=d=1:s=32x32", "-frames:v", "1", cover)

	if err := os.WriteFile(sub, []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = MuxAV(context.Background(), false, "BV1xx411c7mD", video, audio, out,
		"desc", "title", "author", "ep1", cover, "zh-CN",
		[]entity.Subtitle{{Lan: "zh-CN", Path: sub}},
		false, false, false,
		[]entity.ViewPoint{{Title: "开场", Start: 0, End: 1000}, {Title: "正片", Start: 1000, End: 5000}},
		1700000000, false, nil, 5)
	if err != nil {
		t.Fatalf("MuxAV with cover+chapters+subtitle failed: %v", err)
	}

	stat, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if stat.Size() == 0 {
		t.Fatal("output is empty")
	}
}

// TestMp4boxAddsAudioMaterialTracks is the regression for the Dolby Vision path:
// when ffmpeg is older than 5.0 the muxer switches to mp4box automatically, and
// that branch had no audioMaterial argument at all — dubbing / background tracks
// were dropped from the output and then deleted from disk, losing downloaded
// data with no warning (upstream v1.6.17).
func TestMp4boxAddsAudioMaterialTracks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake mp4box is a POSIX shell script")
	}

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "fake-mp4box")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	origMp4box := MP4BOX
	MP4BOX = script
	defer func() { MP4BOX = origMp4box }()

	material := filepath.Join(dir, "dub.m4a")
	err := MuxAV(context.Background(), true, "BV1xx411c7mD",
		filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.m4a"), filepath.Join(dir, "out.mp4"),
		"desc", "title", "author", "ep1", "", "zh-CN", nil,
		false, false, false, nil, 1700000000, false,
		[]entity.AudioMaterial{{Title: "配音", PersonName: "up", Path: material}}, 5)
	if err != nil {
		t.Fatalf("MuxAV via mp4box: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	added, named := false, false
	for _, a := range args {
		if strings.HasPrefix(a, material+":lang=") {
			added = true
		}
		if strings.Contains(a, "type=name:str=") && strings.Contains(a, "配音") {
			named = true
		}
	}
	if !added {
		t.Errorf("the dubbing track never reached the mp4box -add chain\nargs: %v", args)
	}
	if !named {
		t.Errorf("the dubbing track was added without its name\nargs: %v", args)
	}
}

// TestMergeFLVCleansIntermediatesOnFailure: converting N segments produces N-1
// throwaway .ts files; when the combine step fails they used to be left behind
// (gigabytes accumulating across retries). A failure must clean them up.
func TestMergeFLVCleansIntermediatesOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ffmpeg is a POSIX shell script")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg")
	// Writes a non-empty file to the last argument (the .ts output path).
	body := "#!/bin/sh\nprev=\"\"\nfor a in \"$@\"; do prev=\"$a\"; done\necho data > \"$prev\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := FFMPEG
	FFMPEG = script
	defer func() { FFMPEG = orig }()

	var inputs []string
	for _, name := range []string{"seg0.flv", "seg1.flv"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("flv"), 0o644); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, p)
	}

	// 让合并步骤失败：把输出的父路径做成一个普通文件——合并会自动创建缺失的目录
	// （上游 BBDownUtil 的同一契约），所以只能靠"父路径不是目录"来制造失败。
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(blocker, "out.mp4")
	if err := MergeFLV(context.Background(), inputs, out); err == nil {
		t.Fatal("expected the combine step to fail")
	}

	left, err := filepath.Glob(filepath.Join(dir, "*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("intermediate .ts files survived a failed merge: %v", left)
	}
}

// TestCheckFFmpegDOVITimesOutOnHungBinary: the version probe runs on the muxing
// critical path, so a hung binary must not stall the download.
func TestCheckFFmpegDOVITimesOutOnHungBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ffmpeg is a POSIX shell script")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "hanging-ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	origFFmpeg, origTimeout := FFMPEG, doviProbeTimeout
	FFMPEG = script
	doviProbeTimeout = 200 * time.Millisecond
	defer func() { FFMPEG = origFFmpeg; doviProbeTimeout = origTimeout }()

	done := make(chan bool, 1)
	go func() { done <- CheckFFmpegDOVI() }()
	select {
	case supported := <-done:
		if supported {
			t.Error("a hung probe reported Dolby Vision support")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CheckFFmpegDOVI hung: the probe timeout did not fire")
	}
}

// TestEscapeStringFoldsLineBreaks: the value is embedded in mp4box's own
// -itags/-add token syntax, where a raw newline breaks the token.
func TestEscapeStringFoldsLineBreaks(t *testing.T) {
	if got := escapeString("a\nb"); got != "a b" {
		t.Errorf("newline = %q, want a folded space", got)
	}
	if got := escapeString("C:\\cover.jpg"); got != "C:\\\\cover.jpg" {
		t.Errorf("backslash = %q", got)
	}
	if got := escapeString("say \"hi\""); got != "say \\\"hi\\\"" {
		t.Errorf("quote = %q", got)
	}
}

// TestMp4boxEscapesCoverPath: a Windows cover path reached -itags verbatim, so a
// backslash was consumed as an escape sequence and the cover silently vanished
// (upstream RF-6).
func TestMp4boxEscapesCoverPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake mp4box is a POSIX shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "fake-mp4box")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := MP4BOX
	MP4BOX = script
	defer func() { MP4BOX = orig }()

	if err := MuxAV(context.Background(), true, "BV1xx411c7mD",
		filepath.Join(dir, "v.mp4"), "", filepath.Join(dir, "out.mp4"),
		"desc", "title", "author", "ep1", "C:\\cover.jpg", "zh-CN", nil,
		false, false, false, nil, 1700000000, false, nil, 5); err != nil {
		t.Fatalf("MuxAV: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "cover=\"C:\\\\cover.jpg\"") {
		t.Errorf("the cover path was not escaped in -itags:\n%s", raw)
	}
}
