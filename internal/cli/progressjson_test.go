package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/spf13/cobra"
)

// F5 的 CLI 半边：开关本身与「开关 → 下载层」的接线。

// TestProgressJSONFlagRegisteredOnDownloadCommands: --progress-json 必须出现在会下载的
// 命令上（主命令、稍后再看、订阅检查），且默认关闭。
func TestProgressJSONFlagRegisteredOnDownloadCommands(t *testing.T) {
	for _, c := range []*cobra.Command{rootCmd, watchLaterCmd, subCheckCmd} {
		f := c.Flags().Lookup("progress-json")
		if f == nil {
			t.Errorf("%s 上没有 --progress-json 开关（GUI/自动化无从打开 JSON 进度）", c.Name())
			continue
		}
		if f.Value.Type() != "bool" || f.DefValue != "false" {
			t.Errorf("%s: --progress-json 应为默认关闭的布尔开关，实际 type=%s default=%s", c.Name(), f.Value.Type(), f.DefValue)
		}
	}
}

// captureStderr 把 os.Stderr 换成管道，收集 fn 期间写进去的内容。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestApplyProgressJSONWiresDownloadLayer: CLI 的开关必须真的传到下载层——
// 只注册旗标不接线的话，用户加了 --progress-json 也什么都不会发生。
// 用一次本地回环下载验证两个方向（开 → 有事件；关 → 没有事件）。
//
// 变异验证：把 applyProgressJSON 改成空函数，「开」的那一半拿不到任何事件，本用例变红。
func TestApplyProgressJSONWiresDownloadLayer(t *testing.T) {
	body := bytes.Repeat([]byte("m"), 2<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "media.bin", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	t.Cleanup(func() { optProgressJSON = false; applyProgressJSON() })

	run := func(enabled bool) string {
		optProgressJSON = enabled
		applyProgressJSON()
		cfg := download.DownloadConfig{
			Client:     util.NewHTTPClient(nil, func() string { return "" }, nil),
			RetryCount: 1,
		}
		return captureStderr(t, func() {
			if err := download.DownloadFile(context.Background(), srv.URL+"/media.bin", filepath.Join(t.TempDir(), "o.bin"), cfg); err != nil {
				t.Fatalf("下载失败: %v", err)
			}
		})
	}

	if out := run(true); !strings.Contains(out, "\"state\"") {
		t.Errorf("--progress-json 打开后下载层没有任何 JSON 事件（开关没接上）: %q", out)
	}
	if out := run(false); out != "" {
		t.Errorf("--progress-json 关闭后不该有 JSON 事件: %q", out)
	}
}
