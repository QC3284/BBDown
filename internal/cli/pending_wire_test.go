package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/spf13/cobra"
)

// 失败的目标必须被登记进未完成任务清单——这是 bbdown resume 能「接着下」的前提。
//
// 变异验证：去掉 downloadTargets 里的 upsertPending 调用 → 本用例变红。
func TestDownloadTargetsRecordsFailuresForResume(t *testing.T) {
	// 不用 t.TempDir()：Windows 上它会因「目录里还有被占用的文件」在 cleanup 阶段报错
	// （实测 windows-latest：unlinkat ... The process cannot access the file），
	// 那是平台的句柄语义，与被测行为无关。这里自己建目录并**尽力清理**，失败不影响结论。
	dir, err := os.MkdirTemp("", "bbdown-pending-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// 假 host 一律 412：目标必然失败，且失败得很快（不需要真实网络）。
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()

	cfg := config.DefaultMyOption()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.WorkDir = dir
	cfg.RetryDelay = 1
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)

	runErr := downloadTargets(context.Background(), &cobra.Command{}, cfg, client, []string{"BV1xx411c7mD"})
	if runErr == nil {
		t.Fatal("目标应当失败（假 host 一律 412）")
	}

	tasks, lerr := loadPending(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(tasks) != 1 || tasks[0].URL != "BV1xx411c7mD" {
		t.Fatalf("失败目标应当被登记，实际 %v", tasks)
	}
	if tasks[0].LastErr == "" {
		t.Error("登记项应当带上最后一次错误（resume 时要提示用户）")
	}
}
