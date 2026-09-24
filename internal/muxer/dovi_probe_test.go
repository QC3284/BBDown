package muxer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// O6：混流阶段的进程启动。两件事：
//
//	① 版本探测的正则此前把反斜杠丢了（写成 libavutils+(d+). +(d+).），永远匹配不到 ffmpeg 的
//	   "libavutil  58.  2.100" → CheckFFmpegDOVI 恒 false → 杜比视界一律回退 mp4box（没装 mp4box
//	   的机器直接混流失败）；
//	② 探测要启动一次 ffmpeg 进程且在混流关键路径上，多P 杜比视界此前每P都探一次。
//
// 变异验证：把正则改回丢反斜杠的字面量 → 第一段变红；去掉 doviProbeCache → 第二段变红。
func fakeFFmpeg(t *testing.T, output string, logFile string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ffmpeg is a POSIX shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg")
	body := "#!/bin/sh\n"
	if logFile != "" {
		body += "printf 'x\n' >> '" + logFile + "'\n"
	}
	body += "printf '%s\n' '" + output + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func withFFmpeg(t *testing.T, path string) {
	t.Helper()
	orig := FFMPEG
	FFMPEG = path
	t.Cleanup(func() { FFMPEG = orig })
}

func TestCheckFFmpegDOVIParsesVersionOutput(t *testing.T) {
	withFFmpeg(t, fakeFFmpeg(t, "ffmpeg version 6.0 Copyright\n  libavutil      58.  2.100 / 58.  2.100", ""))
	if !CheckFFmpegDOVI() {
		t.Error("libavutil 58 必须判定为支持杜比视界混流（此前正则丢反斜杠，恒为 false）")
	}

	// 边界取自上游：> 57 或 57.17+ 才算支持（≈ ffmpeg 6.0+）。
	for _, c := range []struct {
		major, minor int
		want         bool
	}{
		{58, 2, true},
		{57, 17, true},
		{57, 16, false},
		{56, 70, false},
	} {
		out := fmt.Sprintf("ffmpeg version x\n  libavutil      %d. %d.100 / %d. %d.100", c.major, c.minor, c.major, c.minor)
		withFFmpeg(t, fakeFFmpeg(t, out, ""))
		if got := CheckFFmpegDOVI(); got != c.want {
			t.Errorf("libavutil %d.%d: got %v want %v（上游规则：>57 或 57.17+）", c.major, c.minor, got, c.want)
		}
	}
}

func TestCheckFFmpegDOVIProbesOncePerBinary(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "probes.log")
	withFFmpeg(t, fakeFFmpeg(t, "  libavutil      58.  2.100", logFile))

	for i := 0; i < 3; i++ {
		if !CheckFFmpegDOVI() {
			t.Fatal("探测应当为 true")
		}
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("假 ffmpeg 没有被调用过：%v", err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Errorf("同一二进制应只探测一次，实际调用 %d 次（多P 杜比视界每P探一次）", got)
	}
}
