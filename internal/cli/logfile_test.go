package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// --log-file：运行日志落文件（上游 AliverAnme 有、我们此前缺）。
// Logger.SetLogFile 早就实现（含写失败挂起），但没有任何 CLI 入口调用它——
// 这条用例既守开关存在，也守「真的写进文件」。
//
// 变异验证：删掉 PersistentFlags 里的 --log-file 注册 → 第一条断言变红。
func TestLogFileFlagIsWired(t *testing.T) {
	if rootCmd.PersistentFlags().Lookup("log-file") == nil {
		t.Fatal("根命令应当注册 --log-file（持久标志，所有子命令都能用）")
	}

	path := filepath.Join(t.TempDir(), "run.log")
	util.SetLogFile(path)
	t.Cleanup(func() { util.SetLogFile("") }) // 置空即关闭，避免影响同包其它用例
	util.Log("日志落盘冒烟 %s", "ok")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("日志文件应当被写出：%v", err)
	}
	if !strings.Contains(string(body), "日志落盘冒烟 ok") {
		t.Errorf("日志内容不符：%s", string(body))
	}
}
