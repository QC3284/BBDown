package drm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件搬上游 WvdDeviceKeyTests 的可移植部分（错误路径）：损坏/截断/空的 .wvd
// 必须给出可读诊断，而不是静默泄漏或抛底层索引越界。

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpstreamLoadWvdDeviceErrors(t *testing.T) {
	// 空文件
	if _, err := LoadWvdDevice(writeTemp(t, "empty.wvd", nil)); err == nil {
		t.Error("空 .wvd 应报错")
	} else if !strings.Contains(err.Error(), "为空") {
		t.Errorf("空文件应给出「为空」诊断，实际 %q", err)
	}

	// 损坏数据（既不是 WVD 头也不是 v1/v2/PEM）
	if _, err := LoadWvdDevice(writeTemp(t, "garbage.wvd", []byte{0x01, 0x02, 0x03})); err == nil {
		t.Error("损坏的 .wvd 应报错")
	}

	// 截断：私钥长度声明超出实际可用字节
	// 布局（无 WVD 头时）：[version][?][?][flags][keyLen u16][key][clientIdLen u16][clientId]
	truncated := []byte{1, 0, 0, 0, 0xFF, 0xFF}
	if _, err := LoadWvdDevice(writeTemp(t, "truncated.wvd", truncated)); err == nil {
		t.Error("截断的 .wvd 应报错")
	} else if !strings.Contains(err.Error(), "超出") && !strings.Contains(err.Error(), "截断") {
		t.Errorf("截断应给出可读诊断，实际 %q", err)
	}

	// v2 且私钥加密：不支持，必须显式拒绝而不是当作可用设备
	encrypted := []byte{2, 0, 0, 0x01, 0, 0}
	if _, err := LoadWvdDevice(writeTemp(t, "enc.wvd", encrypted)); err == nil {
		t.Error("加密的 v2 .wvd 应报错")
	} else if !strings.Contains(strings.ToLower(err.Error()), "encrypt") {
		t.Errorf("加密设备应明确说明不支持，实际 %q", err)
	}
}
