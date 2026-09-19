package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// TestStartDownloadingDebugLineMatchesUpstream 钉住上游那条「每文件一次」的 Debug：
// BBDownDownloadUtil.DownloadFileCoreAsync 与 MultiThreadDownloadCoreAsync 都会打
// 「Start downloading: <脱敏 URL>」。本仓此前一行都没有——用户报「404 概率比较高」时，
// 日志里看不出是哪台 host/哪条签名 URL 失败，「强制替换镜像后 404」这类假设无法证实。
func TestStartDownloadingDebugLineMatchesUpstream(t *testing.T) {
	util.SetDefaultDebugFn(func() bool { return true })
	t.Cleanup(func() { util.SetDefaultDebugFn(func() bool { return false }) })

	body := make([]byte, 4<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "media.bin", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()

	cases := []struct {
		name string
		cfg  DownloadConfig
	}{
		{"单线程", DownloadConfig{Client: newTestClient()}},
		{"多线程", DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := srv.URL + "/a.m4s?sign=SECRET123456&deadline=99"
			out := captureStdout(t, func() {
				if err := DownloadFile(context.Background(), url, filepath.Join(t.TempDir(), "o.bin"), c.cfg); err != nil {
					t.Fatalf("下载失败: %v", err)
				}
			})
			if got := strings.Count(out, "Start downloading:"); got != 1 {
				t.Errorf("Start downloading 行出现 %d 次，期望 1 次（上游每文件一次）：%q", got, out)
			}
			if !strings.Contains(out, "/a.m4s") {
				t.Errorf("这一行必须带上（脱敏后的）URL 才能定位 404 的 host/路径：%q", out)
			}
			if strings.Contains(out, "SECRET123456") {
				t.Errorf("签名参数必须脱敏后再进日志：%q", out)
			}
		})
	}
}
