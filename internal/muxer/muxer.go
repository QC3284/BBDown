package muxer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// MuxAV merges audio and video tracks using ffmpeg or mp4box.
func MuxAV(ctx context.Context, useMp4box bool, bvid, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang string, subs []entity.Subtitle, audioOnly, videoOnly, simplyMux bool, points []entity.ViewPoint, pubTime int64, isHevc bool) error {
	if audioOnly && audioPath != "" {
		videoPath = ""
	}
	if videoOnly {
		audioPath = ""
	}

	url := "https://www.bilibili.com/video/" + bvid + "/"

	if useMp4box {
		return muxByMp4box(ctx, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang, subs, audioOnly, videoOnly, points)
	}
	return muxByFFmpeg(ctx, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang, subs, audioOnly, videoOnly, simplyMux, points, pubTime, isHevc)
}

func muxByFFmpeg(ctx context.Context, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang string, subs []entity.Subtitle, audioOnly, videoOnly, simplyMux bool, points []entity.ViewPoint, pubTime int64, isHevc bool) error {
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
				fmt.Sprintf("-metadata:s:s:%d", subIdx), "title="+langName,
				fmt.Sprintf("-metadata:s:s:%d", subIdx), "language="+langCode,
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

	for i := 0; i < inputCount; i++ {
		args = append(args, "-map", fmt.Sprintf("%d", i))
	}

	args = append(args, "-loglevel", "warning", "-y")

	if !simplyMux {
		metaTitle := episodeID
		if metaTitle == "" {
			metaTitle = title
		}
		args = append(args, "-metadata", "title="+metaTitle, "-metadata", "comment="+url)
		if lang != "" {
			args = append(args, "-metadata:s:a:0", "language="+lang)
		}
		if desc != "" {
			args = append(args, "-metadata", "description="+desc)
		}
		if author != "" {
			args = append(args, "-metadata", "artist="+author)
		}
		if episodeID != "" {
			args = append(args, "-metadata", "album="+title)
		}
	}

	args = append(args, "-c:v", "copy", "-c:a", "copy")
	if audioOnly && audioPath == "" {
		args = append(args, "-vn")
	}
	if subs != nil {
		args = append(args, "-c:s", "mov_text")
	}
	args = append(args, "-movflags", "faststart", "-strict", "unofficial", "-strict", "-2", "-f", "mp4", "--", outPath)

	util.LogDebug("ffmpeg: %s %s", "ffmpeg", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func muxByMp4box(ctx context.Context, url, videoPath, audioPath, outPath, desc, title, author, episodeID, pic, lang string, subs []entity.Subtitle, audioOnly, videoOnly bool, points []entity.ViewPoint) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	args := []string{"-inter", "500", "-noprog"}

	if videoPath != "" {
		trackID := "1"
		if audioOnly && audioPath == "" {
			trackID = "2"
		}
		args = append(args, "-add", fmt.Sprintf("%s#trackID=%s:name=", videoPath, trackID))
	}
	if audioPath != "" {
		audioLang := lang
		if audioLang == "" {
			audioLang = "und"
		}
		args = append(args, "-add", fmt.Sprintf("%s:lang=%s", audioPath, audioLang))
	}

	for _, s := range subs {
		if stat, err := os.Stat(s.Path); err == nil && stat.Size() > 0 {
			langCode, langName := util.GetSubtitleCode(s.Lan)
			args = append(args, "-add", fmt.Sprintf("%s#trackID=1:name=%s:hdlr=sbtl:lang=%s", s.Path, langName, langCode))
		}
	}

	args = append(args, "-new", "--", outPath)

	util.LogDebug("mp4box: %s %s", "mp4box", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "mp4box", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// MergeFLV merges FLV segments into a single MP4 file.
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
		util.LogDebug("ffmpeg: %s %s", "ffmpeg", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("FLV segment conversion failed: %w", err)
		}
		tsFiles = append(tsFiles, tsFile)
		os.Remove(file)
	}

	if err := util.CombineMultipleFilesIntoSingleFile(tsFiles, outPath); err != nil {
		return err
	}
	for _, ts := range tsFiles {
		os.Remove(ts)
	}
	return nil
}
