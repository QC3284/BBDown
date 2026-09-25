package cli

import (
	"testing"

	"github.com/QC3284/BBDown/internal/muxer"
)

// setMuxerFFmpeg 临时替换混流工具路径（doctor 用例用；CheckFFmpegDOVI 按路径缓存，互不干扰）。
func setMuxerFFmpeg(t *testing.T, path string) {
	t.Helper()
	orig := muxer.FFMPEG
	muxer.FFMPEG = path
	t.Cleanup(func() { muxer.FFMPEG = orig })
}

// muxerFFmpegForTest 只是为了让用例读起来对称（返回当前值，不做改动）。
func muxerFFmpegForTest(t *testing.T) string {
	t.Helper()
	return muxer.FFMPEG
}
