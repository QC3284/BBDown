package util

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// 本文件把上游 BBDown.Tests/CombineFilesTests.cs 的四条契约搬过来做差分。

func TestUpstreamCombineMergesInOrder(t *testing.T) {
	dir := t.TempDir()
	f1, f2, out := filepath.Join(dir, "a.bin"), filepath.Join(dir, "b.bin"), filepath.Join(dir, "out.bin")
	if err := os.WriteFile(f1, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte{4, 5, 6, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CombineMultipleFilesIntoSingleFile(context.Background(), []string{f1, f2}, out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string([]byte{1, 2, 3, 4, 5, 6, 7}) {
		t.Errorf("合并结果 = %v，上游期望 [1 2 3 4 5 6 7]", got)
	}
}

// 上游：单分片走 MoveTo，源文件应消失（不做无谓的整段拷贝）。
func TestUpstreamCombineSingleFileMovesDirectly(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "solo.bin"), filepath.Join(dir, "solo-out.bin")
	if err := os.WriteFile(src, []byte{9, 8, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CombineMultipleFilesIntoSingleFile(context.Background(), []string{src}, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string([]byte{9, 8, 7}) {
		t.Errorf("内容 = %v", got)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("单分片应走 rename，源文件应消失")
	}
}

// 上游：预取消必须在创建输出前抛错，不留下空文件。
func TestUpstreamCombineCancelledTokenThrows(t *testing.T) {
	dir := t.TempDir()
	f1, f2, out := filepath.Join(dir, "a.bin"), filepath.Join(dir, "b.bin"), filepath.Join(dir, "out.bin")
	_ = os.WriteFile(f1, []byte{1, 2, 3}, 0o644)
	_ = os.WriteFile(f2, []byte{4, 5, 6, 7}, 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CombineMultipleFilesIntoSingleFile(ctx, []string{f1, f2}, out); !errors.Is(err, context.Canceled) {
		t.Errorf("预取消应返回取消错误，实际 %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("预取消不应留下输出文件")
	}
}

// 上游：输入缺失时抛错，并删除半截产物（“合并中途失败应删除半截产物”）。
func TestUpstreamCombineMissingInputRemovesPartialOutput(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.bin")
	missing := filepath.Join(dir, "nope.bin")
	out := filepath.Join(dir, "out.bin")
	_ = os.WriteFile(f1, []byte{1, 2, 3, 4}, 0o644)
	err := CombineMultipleFilesIntoSingleFile(context.Background(), []string{f1, missing}, out)
	if err == nil {
		t.Fatal("输入缺失应报错")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("应为「文件不存在」，实际 %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Error("合并中途失败应删除半截产物")
	}
}

// 上游：输出目录不存在时自动创建。
func TestUpstreamCombineCreatesOutputDirectory(t *testing.T) {
	dir := t.TempDir()
	f1, f2 := filepath.Join(dir, "a.bin"), filepath.Join(dir, "b.bin")
	_ = os.WriteFile(f1, []byte{1}, 0o644)
	_ = os.WriteFile(f2, []byte{2}, 0o644)
	out := filepath.Join(dir, "nested", "deep", "out.bin")
	if err := CombineMultipleFilesIntoSingleFile(context.Background(), []string{f1, f2}, out); err != nil {
		t.Fatalf("输出目录不存在时应自动创建: %v", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != string([]byte{1, 2}) {
		t.Errorf("内容 = %v", got)
	}
}
