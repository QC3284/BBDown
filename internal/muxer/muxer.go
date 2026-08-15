package muxer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// FFMPEG and MP4BOX are configurable external tool paths (set by findBinaries).
var FFMPEG = "ffmpeg"
var MP4BOX = "mp4box"

// MuxAV merges audio and video tracks using ffmpeg or mp4box (upstream BBDownMuxer).
func MuxAV(ctx context.Context, useMp4box bool, bvid, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang string, subs []entity.Subtitle, audioOnly, videoOnly, simplyMux bool, points []entity.ViewPoint, pubTime int64, isHevc bool, audioMaterial []entity.AudioMaterial, timeoutMinutes int) error {
	if audioOnly && audioPath != "" {
		videoPath = ""
	}
	if videoOnly {
		audioPath = ""
	}

	url := "https://www.bilibili.com/video/" + bvid + "/"

	if useMp4box {
		return muxByMp4box(ctx, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang, subs, audioOnly, videoOnly, points, timeoutMinutes)
	}
	return muxByFFmpeg(ctx, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang, subs, audioOnly, videoOnly, simplyMux, points, pubTime, isHevc, audioMaterial, timeoutMinutes)
}

// escapeString escapes backslashes and double quotes (upstream EscapeString).
func escapeString(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, "\"", "\\\"")
}

func muxByFFmpeg(ctx context.Context, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang string, subs []entity.Subtitle, audioOnly, videoOnly, simplyMux bool, points []entity.ViewPoint, pubTime int64, isHevc bool, audioMaterial []entity.AudioMaterial, timeoutMinutes int) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	args := []string{}
	inputCount := 0

	if videoPath != "" {
		args = append(args, "-i", videoPath)
		inputCount++
	}
	if audioPath != "" {
		args = append(args, "-i", audioPath)
		inputCount++
	}

	// Background audio / dubbing tracks (upstream audioMaterial).
	if len(audioMaterial) > 0 {
		args = append(args, "-metadata:s:a:0", "title=原音频")
		audioCount := 0
		for _, a := range audioMaterial {
			args = append(args, "-i", a.Path)
			inputCount++
			audioCount++
			if a.Title != "" {
				args = append(args, fmt.Sprintf("-metadata:s:a:%d", audioCount), "title="+escapeString(a.Title))
			}
			if a.PersonName != "" {
				args = append(args, fmt.Sprintf("-metadata:s:a:%d", audioCount), "artist="+escapeString(a.PersonName))
			}
		}
	}

	if pic != "" {
		args = append(args, "-i", pic)
		inputCount++
	}

	subIdx := 0
	for _, s := range subs {
		if stat, err := os.Stat(s.Path); err == nil && stat.Size() > 0 {
			args = append(args, "-i", s.Path)
			_, langName := util.GetSubtitleCode(s.Lan)
			langCode, _ := util.GetSubtitleCode(s.Lan)
			args = append(args,
				fmt.Sprintf("-metadata:s:s:%d", subIdx), "title="+escapeString(langName),
				fmt.Sprintf("-metadata:s:s:%d", subIdx), "language="+escapeString(langCode),
			)
			inputCount++
			subIdx++
		}
	}

	if pic != "" {
		dispositionIdx := "1"
		if audioOnly {
			dispositionIdx = "0"
		}
		args = append(args, "-disposition:v:"+dispositionIdx, "attached_pic")
	}

	// Chapters (view points) via an FFMETADATA sidecar file.
	metaFile := ""
	if len(points) > 0 {
		baseDir := filepath.Dir(videoPath)
		if baseDir == "" {
			baseDir = filepath.Dir(audioPath)
		}
		if baseDir == "" {
			baseDir = "."
		}
		metaFile = filepath.Join(baseDir, "chapters-"+strings.TrimSuffix(filepath.Base(outPath), filepath.Ext(outPath)))
		if err := os.WriteFile(metaFile, []byte(ffmpegMetaString(points)), 0o644); err != nil {
			return err
		}
		args = append(args, "-i", metaFile)
		args = append(args, "-map_chapters", fmt.Sprintf("%d", inputCount))
	}

	for i := 0; i < inputCount; i++ {
		args = append(args, "-map", fmt.Sprintf("%d", i))
	}

	args = append(args, "-loglevel")
	if util.DebugLogEnabled() {
		args = append(args, "verbose")
	} else {
		args = append(args, "warning")
	}
	args = append(args, "-y")

	if !simplyMux {
		metaTitle := episodeID
		if metaTitle == "" {
			metaTitle = title
		}
		args = append(args, "-metadata", "title="+escapeString(metaTitle))
		args = append(args, "-metadata", "comment="+escapeString(url))
		if lang != "" {
			args = append(args, "-metadata:s:a:0", "language="+escapeString(lang))
		}
		if desc != "" {
			args = append(args, "-metadata", "description="+escapeString(desc))
		}
		if author != "" {
			args = append(args, "-metadata", "artist="+escapeString(author))
		}
		if episodeID != "" {
			args = append(args, "-metadata", "album="+escapeString(title))
		}
		if pubTime != 0 {
			args = append(args, "-metadata", "creation_time="+time.Unix(pubTime, 0).UTC().Format("2006-01-02T15:04:05.000000Z"))
		}
	}

	args = append(args, "-c:v", "copy", "-c:a", "copy")
	if audioOnly && audioPath == "" {
		args = append(args, "-vn")
	}
	if subs != nil {
		args = append(args, "-c:s", "mov_text")
	}
	// fix macOS hev1, see https://discussions.apple.com/thread/253081863
	if runtime.GOOS == "darwin" && isHevc {
		args = append(args, "-tag:v:0", "hvc1")
	}
	args = append(args, "-movflags", "faststart", "-strict", "unofficial", "-strict", "-2", "-f", "mp4", "--", outPath)

	util.LogDebug("ffmpeg: %s %s", FFMPEG, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, FFMPEG, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if metaFile != "" {
		os.Remove(metaFile)
	}
	return err
}

// CheckFFmpegDOVI reports whether ffmpeg supports Dolby Vision muxing
// (libavutil major >= 5, upstream CheckFFmpegDOVI).
func CheckFFmpegDOVI() bool {
	out, err := exec.Command(FFMPEG, "-version").CombinedOutput()
	if err != nil {
		return false
	}
	m := doviVersionRegex.FindStringSubmatch(string(out))
	if m == nil {
		return false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	return major >= 5
}

var doviVersionRegex = regexp.MustCompile(`libavutils+(d+). +(d+).`)

// ffmpegMetaString builds an FFMETADATA chapters file (upstream GetFFmpegMetaString).
func ffmpegMetaString(points []entity.ViewPoint) string {
	var sb strings.Builder
	sb.WriteString(";FFMETADATA\n")
	for _, p := range points {
		const timeBase = 1000
		sb.WriteString("[CHAPTER]\n")
		sb.WriteString(fmt.Sprintf("TIMEBASE=1/%d\n", timeBase))
		sb.WriteString(fmt.Sprintf("START=%d\n", p.Start*timeBase))
		sb.WriteString(fmt.Sprintf("END=%d\n", p.End*timeBase))
		title := strings.NewReplacer("\n", " ", "\r", " ").Replace(p.Title)
		sb.WriteString("title=" + title + "\n")
		sb.WriteString("\n")
	}
	return sb.String()
}

func muxByMp4box(ctx context.Context, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang string, subs []entity.Subtitle, audioOnly, videoOnly bool, points []entity.ViewPoint, timeoutMinutes int) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	args := []string{"-inter", "500", "-noprog"}
	nowID := 0

	if videoPath != "" {
		trackID := "1"
		if audioOnly && audioPath == "" {
			trackID = "2"
		}
		args = append(args, "-add", fmt.Sprintf("%s#trackID=%s:name=", videoPath, trackID))
		nowID++
	}
	if audioPath != "" {
		audioLang := lang
		if audioLang == "" {
			audioLang = "und"
		}
		args = append(args, "-add", fmt.Sprintf("%s:lang=%s", audioPath, audioLang))
		nowID++
	}

	metaFile := ""
	if len(points) > 0 {
		baseDir := filepath.Dir(videoPath)
		if baseDir == "" {
			baseDir = filepath.Dir(audioPath)
		}
		if baseDir == "" {
			baseDir = "."
		}
		metaFile = filepath.Join(baseDir, "chapters-"+strings.TrimSuffix(filepath.Base(outPath), filepath.Ext(outPath)))
		if err := os.WriteFile(metaFile, []byte(mp4boxMetaString(points)), 0o644); err != nil {
			return err
		}
		args = append(args, "-chap", metaFile)
	}

	// -itags metadata blob.
	var metaArg strings.Builder
	metaArg.WriteString("tool=")
	if pic != "" {
		metaArg.WriteString(":cover=\"")
		metaArg.WriteString(pic)
		metaArg.WriteString("\"")
	}
	if episodeID != "" {
		metaArg.WriteString(":album=\"")
		metaArg.WriteString(escapeString(title))
		metaArg.WriteString("\":title=\"")
		metaArg.WriteString(escapeString(episodeID))
		metaArg.WriteString("\"")
	} else {
		metaArg.WriteString(":title=\"")
		metaArg.WriteString(escapeString(title))
		metaArg.WriteString("\"")
	}
	metaArg.WriteString(":sdesc=\"")
	metaArg.WriteString(escapeString(desc))
	metaArg.WriteString("\":comment=\"")
	metaArg.WriteString(escapeString(url))
	metaArg.WriteString("\":artist=\"")
	metaArg.WriteString(escapeString(author))
	metaArg.WriteString("\"")
	if metaArg.Len() > len("tool=") {
		args = append(args, "-itags", metaArg.String())
	}

	for _, s := range subs {
		if stat, err := os.Stat(s.Path); err == nil && stat.Size() > 0 {
			nowID++
			langCode, langName := util.GetSubtitleCode(s.Lan)
			args = append(args, "-add", fmt.Sprintf("%s#trackID=1:name=%s:hdlr=sbtl:lang=%s", s.Path, langName, langCode))
			args = append(args, "-udta", fmt.Sprintf("%d:type=name:str=\"%s\"", nowID, escapeString(langName)))
		}
	}

	if util.DebugLogEnabled() {
		args = append(args, "-v")
	}
	args = append(args, "-new", "--", outPath)

	util.LogDebug("mp4box: %s %s", MP4BOX, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, MP4BOX, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if metaFile != "" {
		os.Remove(metaFile)
	}
	return err
}

// mp4boxMetaString builds an mp4box chapter file (upstream GetMp4boxMetaString).
func mp4boxMetaString(points []entity.ViewPoint) string {
	var sb strings.Builder
	for _, p := range points {
		title := strings.NewReplacer("\n", " ", "\r", " ").Replace(p.Title)
		sb.WriteString(fmt.Sprintf("%s %s\n", util.FormatTime(p.Start, true), title))
	}
	return sb.String()
}

// MergeFLV merges FLV segments into a single MP4 file with validation (upstream).
func MergeFLV(ctx context.Context, files []string, outPath string) error {
	if len(files) == 0 {
		return nil
	}
	if len(files) == 1 {
		return os.Rename(files[0], outPath)
	}

	var tsFiles []string
	for _, file := range files {
		tsFile := strings.TrimSuffix(file, filepath.Ext(file)) + ".ts"
		args := []string{"-loglevel", "warning", "-y", "-i", file, "-map", "0", "-c", "copy", "-f", "mpegts", "-bsf:v", "h264_mp4toannexb", tsFile}
		util.LogDebug("ffmpeg: %s %s", FFMPEG, strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, FFMPEG, args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("FLV segment conversion failed: %w", err)
		}
		if info, err := os.Stat(tsFile); err != nil || info.Size() == 0 {
			return fmt.Errorf("FLV segment conversion produced no output")
		}
		tsFiles = append(tsFiles, tsFile)
	}

	if err := util.CombineMultipleFilesIntoSingleFile(tsFiles, outPath); err != nil {
		return err
	}
	// All conversions succeeded: clean up .ts intermediates and sources.
	for _, ts := range tsFiles {
		os.Remove(ts)
	}
	for _, file := range files {
		os.Remove(file)
	}
	return nil
}
