package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// bbdown resume 的登记层：只留未完成目标、同 URL 去重、成功后移除、损坏清单要报错而不是静默丢任务。
//
// 变异验证：把 loadPending 的损坏分支改成返回空清单 → 「损坏清单必须报错」那条变红。
func TestPendingRegistry(t *testing.T) {
	dir := t.TempDir()

	// 初始为空（文件不存在不是错误）。
	tasks, err := loadPending(dir)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("空目录应当得到空清单，实际 %v（err=%v）", tasks, err)
	}

	if err := upsertPending(dir, "BV1", "网络中断"); err != nil {
		t.Fatal(err)
	}
	if err := upsertPending(dir, "BV2", "412 风控"); err != nil {
		t.Fatal(err)
	}
	// 同 URL 再记一次：去重并刷新错误。
	if err := upsertPending(dir, "BV1", "磁盘满"); err != nil {
		t.Fatal(err)
	}
	tasks, err = loadPending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("清单应当有 2 条（去重后），实际 %v", tasks)
	}
	for _, task := range tasks {
		if task.URL == "BV1" && task.LastErr != "磁盘满" {
			t.Errorf("同 URL 再记一次应当刷新错误，实际 %q", task.LastErr)
		}
	}

	// 成功后移除；清空后文件应当被删掉。
	if err := removePending(dir, "BV1"); err != nil {
		t.Fatal(err)
	}
	if err := removePending(dir, "BV2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFileName)); !os.IsNotExist(err) {
		t.Errorf("清单清空后文件应当被删除，实际 err=%v", err)
	}

	// 损坏清单：报错，不静默丢任务。
	if err := os.WriteFile(filepath.Join(dir, pendingFileName), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPending(dir); err == nil {
		t.Error("清单损坏时必须报错（静默返回空清单会让用户以为没有待办）")
	}
}
