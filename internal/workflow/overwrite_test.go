package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

// 产物存在性判定：默认（上游语义）存在且非空就跳过；--overwrite 由调用方短路（永不跳过）。
//
// 变异验证：把 shouldSkip 的 `overwrite` 短路删掉 → 「--overwrite 必须重下」那条断言变红。
func TestSkipExistingProduct(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "done.mp4")
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.mp4")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.mp4")

	// 上游语义
	if !skipExistingProduct(existing) {
		t.Error("已存在且非空的产物应当被跳过")
	}
	if skipExistingProduct(empty) {
		t.Error("空产物不该被当成已完成")
	}
	if skipExistingProduct(missing) {
		t.Error("不存在的产物不该被跳过")
	}

	// --overwrite：调用生产代码里的同一条判定（不是用例里复制的副本）
	if shouldSkipProduct(true, existing) {
		t.Error("--overwrite 时必须重下（不跳过）")
	}
	if !shouldSkipProduct(false, existing) {
		t.Error("默认必须在产物已存在时跳过")
	}
}
