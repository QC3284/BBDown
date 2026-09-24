package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// F3 产物校验：落盘字节数必须与「接口声明」一致。可靠来源只有 HTTP 声明（Content-Length /
// Content-Range 的权威总长）与实际写入的字节数：playurl 的 dash 轨道在现行 web 响应里根本没有
// size 字段（真实响应实测），bandwidth × 时长只是估算（实测偏差 0.03%~1.4%），不能拿来做判定。

// TestDownloadDiscardsStaleTmpWhenDeclaredSizeUnknown：HEAD 不被支持时声明长度未知，磁盘上
// 上一次中断留下的 .tmp 无从校验。旧代码只在 2xx 分支删 .tmp，206 分支（offset 为 0 时同样
// 从 0 写入）不删也不截断：产物 = 本次内容 + 旧尾部，比本次实际写入的字节还长，末尾是上一份
// 资源的残留（--skip-mux 保留原始轨道时它就是交付物）。声明长度未知时本地前缀一律不可信是
// 无条件的，不该依赖"哪个状态码分支顺手删了它"。
//
// 变异验证：去掉 singleDownload 里 pr.size <= 0 的丢弃分支，本用例变红（产物 4096 字节）。
func TestDownloadDiscardsStaleTmpWhenDeclaredSizeUnknown(t *testing.T) {
	body := []byte("0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed) // 服务器不支持 HEAD：声明长度未知
			return
		}
		// 不带 Range 的请求也可能被回 206（代码本来就单独处理了这个分支）：不截断就会留旧尾部。
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	stale := bytes.Repeat([]byte("X"), 4096)
	if err := os.WriteFile(dest+".tmp", stale, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1, RetryDelayMs: 1}
	if err := DownloadFile(context.Background(), srv.URL+"/a.m4s", dest, cfg); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("产物应恰好是本次写入的 %d 字节，实际 %d 字节（陈旧 .tmp 的尾部被留在了产物里）", len(body), len(got))
	}
}

// TestSingleThreadRejectsConflictingContentRangeTotal：HEAD 声明 1000 字节、Range 响应声明的
// 权威总长却是 2000 字节——两次声明互相矛盾，产物不可信。旧行为会把这份长度不对的内容落盘，
// 只是恰好被末尾的长度检查拦下（错误信息也说不清是哪一份声明不对）。
//
// 变异验证：去掉 206 分支里的 rangeTotal 校验，本用例变红（下载判成功）。
func TestSingleThreadRejectsConflictingContentRangeTotal(t *testing.T) {
	const probed = 1000
	const served = 2000
	const offset = 400

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(probed))
			w.WriteHeader(http.StatusOK)
			return
		}
		// 服务器按 Range 给内容，但声明的总长与 HEAD 探测到的不是同一个对象。
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, probed-1, served))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, probed-offset))
	}))
	defer srv.Close()

	url := srv.URL + "/v.m4s?deadline=1&sign=old"
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := os.WriteFile(dest+".tmp", make([]byte, offset), 0o644); err != nil {
		t.Fatal(err)
	}
	// 续传清单：身份用稳定身份（剥离会刷新的签名参数），长度与探测一致 → .tmp 被信任并续传。
	manifest, err := json.Marshal(resumeManifest{Identity: stableResourceIdentity(url), URL: url, Size: probed})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".tmp.meta", manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1, RetryDelayMs: 1}
	dlErr := DownloadFile(context.Background(), url, dest, cfg)
	if dlErr == nil {
		t.Fatal("两次声明的总长互相矛盾时必须报错，不能把内容当成完整产物交付")
	}
	if !strings.Contains(dlErr.Error(), "两次声明的长度不一致") {
		t.Errorf("错误信息应指出两次声明不一致，实际 %q", dlErr.Error())
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("长度矛盾的下载不得落成最终产物")
	}
}

// TestMultiThreadRejectsClipWithConflictingTotal：HEAD 声明 4MB，每个分片的 206 却声明 8MB
// ——拼出来的只是另一份资源的 4MB 前缀，而每个分片的字节数都"对"，旧行为会当成功合并出去。
//
// 变异验证：去掉 downloadRange 里的 rangeTotal 校验，本用例变红（合并成功、产物落盘）。
func TestMultiThreadRejectsClipWithConflictingTotal(t *testing.T) {
	const total = int64(4) << 20

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
			w.WriteHeader(http.StatusOK)
			return
		}
		parts := strings.SplitN(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-", 2)
		from, _ := strconv.ParseInt(parts[0], 10, 64)
		to, _ := strconv.ParseInt(parts[1], 10, 64)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, total*2))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, to-from+1))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), MultiThread: true, SegmentSizeMB: 1, RetryCount: 1, RetryDelayMs: 1}
	err := DownloadFile(context.Background(), srv.URL+"/v.mp4", dest, cfg)
	if err == nil {
		t.Fatal("分片声明的总长与探测不一致时必须报错，不能把另一份资源的前缀合并成产物")
	}
	if !strings.Contains(err.Error(), "分片总长与探测不一致") {
		t.Errorf("错误信息应指出分片总长与探测不一致，实际 %q", err.Error())
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("总长矛盾的分片不得合并落成产物")
	}
}

// TestAria2cVerifiesDeclaredSize：aria2c 只给退出码、自己不核对长度。假 aria2c 写出截断产物
// （或恰好写满）时，下载器必须按服务器声明的长度复核。
//
// 变异验证：去掉 downloadWithAria2c 末尾的长度复核，截断那一半变红（nil）。
func TestAria2cVerifiesDeclaredSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake aria2c is a POSIX shell script")
	}
	const declared = 8192

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(declared))
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(make([]byte, declared))
	}))
	defer cdn.Close()

	cases := []struct {
		name    string
		script  string
		wantErr bool
	}{
		{"截断的产物", "printf 'truncated' > \"%s\"\n", true},
		{"长度正确的产物", "dd if=/dev/zero of=\"%s\" bs=1024 count=8 2>/dev/null\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "out.bin")
			script := filepath.Join(dir, "aria2c")
			if err := os.WriteFile(script, []byte("#!/bin/sh\n"+fmt.Sprintf(c.script, dest)), 0o755); err != nil {
				t.Fatal(err)
			}

			cfg := DownloadConfig{Client: newTestClient(), UseAria2c: true, Aria2cPath: script}
			err := DownloadFile(context.Background(), cdn.URL+"/a.m4s", dest, cfg)
			if c.wantErr {
				if err == nil {
					t.Fatal("aria2c 产物长度与服务器声明不符时必须报错")
				}
				if !strings.Contains(err.Error(), "aria2 产物长度") {
					t.Errorf("错误信息应指出产物长度不符，实际 %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("长度一致的产物不该报错: %v", err)
			}
			if info, statErr := os.Stat(dest); statErr != nil || info.Size() != declared {
				t.Errorf("产物大小 = %v (err=%v)，期望 %d", info, statErr, declared)
			}
		})
	}
}
