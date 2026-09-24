package download

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// O5：断点续传命中率。上游 v1.6.20 用 StableResourceIdentity（剥离会刷新的签名参数）比较清单，
// 本仓此前比完整 URL——B 站每次解析都会换 deadline/sign/trid/upsig，于是跨进程续传**永不命中**。

func TestStableResourceIdentityStripsSignatureParams(t *testing.T) {
	a := "https://upos.example.com/video.mp4?mid=1&deadline=1700000000&sign=abc&wts=1700000000&qn=80"
	b := "https://upos.example.com/video.mp4?mid=1&deadline=1700000300&sign=def&wts=1700000300&qn=80"
	if stableResourceIdentity(a) != stableResourceIdentity(b) {
		t.Errorf("同一资源刷新签名后身份必须相同：%q vs %q", stableResourceIdentity(a), stableResourceIdentity(b))
	}
	stable := stableResourceIdentity(a)
	for _, want := range []string{"mid=1", "qn=80"} {
		if !strings.Contains(stable, want) {
			t.Errorf("稳定参数 %s 必须保留：%q", want, stable)
		}
	}
	for _, bad := range []string{"sign=", "deadline=", "wts="} {
		if strings.Contains(stable, bad) {
			t.Errorf("签名参数 %s 必须剥离：%q", bad, stable)
		}
	}
	if stableResourceIdentity("https://upos.example.com/other.mp4?mid=1&deadline=1&sign=x") == stable {
		t.Error("不同路径必须是不同身份")
	}
}

func TestManifestMatchesProbe(t *testing.T) {
	const ident = "https://cdn.example.com/1080p.mp4?qn=80"
	mk := func(identity, url string, size int64, etag, lm string) resumeManifest {
		return resumeManifest{Identity: identity, URL: url, Size: size, ETag: etag, LastModified: lm}
	}
	cases := []struct {
		name               string
		m                  resumeManifest
		identity           string
		size               int64
		etag, lastModified string
		want               bool
	}{
		{"同身份同长", mk(ident, "", 12345, "", ""), ident, 12345, "", "", true},
		{"旧清单只存完整 URL，按稳定身份回退匹配", mk("", "https://cdn.example.com/1080p.mp4?qn=80&deadline=1", 12345, "", ""), ident, 12345, "", "", true},
		{"不同资源（路径不同）", mk("https://cdn.example.com/720p.mp4", "", 12345, "", ""), ident, 12345, "", "", false},
		{"总长不一致", mk(ident, "", 999, "", ""), ident, 12345, "", "", false},
		{"ETag 双方都有且不同", mk(ident, "", 12345, "W/old", ""), ident, 12345, "W/new", "", false},
		{"ETag 单边缺失不拦", mk(ident, "", 12345, "", ""), ident, 12345, "W/new", "", true},
		{"Last-Modified 双方都有且不同", mk(ident, "", 12345, "", "Mon, 01 Jan 2024 00:00:00 GMT"), ident, 12345, "", "Tue, 02 Jan 2024 00:00:00 GMT", false},
	}
	for _, c := range cases {
		if got := manifestMatchesProbe(c.m, c.identity, c.size, c.etag, c.lastModified); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// TestResumeHitsAcrossRefreshedSignature 是 O5 的数字来源：第一次下载被中断（服务端发一半就断连），
// 第二次带**刷新后的签名**重跑，必须只补缺口（Range: bytes=N-），而不是从头重下。
//
// 变异验证：身份比较改回完整 URL（manifestMatchesProbe 里比 m.URL），第二段会变成整份重下，本用例变红。
func TestResumeHitsAcrossRefreshedSignature(t *testing.T) {
	const total = 2 << 20
	body := make([]byte, total)
	for i := range body {
		body[i] = byte(i % 251)
	}
	const cut = 512 << 10

	var mu sync.Mutex
	var ranges []string
	var servedSecondRun int64
	firstRun := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("ETag", "v1") // 用例只比较字符串，不入 RFC 的引号要求
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(total))
			w.WriteHeader(http.StatusOK)
			return
		}
		rng := r.Header.Get("Range")
		ranges = append(ranges, rng)
		if firstRun {
			// 只发一半就断连：模拟中断（客户端拿到 unexpected EOF），.tmp 与清单留在磁盘上。
			w.Header().Set("Content-Length", fmt.Sprint(cut))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body[:cut])
			firstRun = false
			panic(http.ErrAbortHandler)
		}
		start := int64(0)
		if strings.HasPrefix(rng, "bytes=") {
			_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), "%d", &start)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
		w.Header().Set("Content-Length", fmt.Sprint(total-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[start:])
		servedSecondRun += total - start
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	cfg := DownloadConfig{Client: newTestClient(), RetryCount: 1, RetryDelayMs: 1}

	// 第一段：旧签名，中途断连 → 失败但留下 .tmp 与清单。
	err := DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=1&sign=old&qn=80", dest, cfg)
	if err == nil {
		t.Fatal("第一段应当失败（服务端中途断连）")
	}
	partial, statErr := os.Stat(dest + ".tmp")
	if statErr != nil || partial.Size() == 0 {
		t.Fatalf("中断后应留下部分 .tmp：%v", statErr)
	}

	// 第二段：签名刷新（同一资源）→ 必须从断点续传。
	if err := DownloadFile(context.Background(), srv.URL+"/v.mp4?deadline=999&sign=new&qn=80", dest, cfg); err != nil {
		t.Fatalf("第二段应当续传成功：%v", err)
	}
	mu.Lock()
	secondRanges := append([]string(nil), ranges...)
	secondServed := servedSecondRun
	mu.Unlock()

	got, readErr := os.ReadFile(dest)
	if readErr != nil || !bytes.Equal(got, body) {
		t.Fatalf("续传产物不完整或内容不符：err=%v 大小=%d", readErr, len(got))
	}
	if len(secondRanges) < 2 || !strings.HasPrefix(secondRanges[1], "bytes=") {
		t.Errorf("第二段应当发 Range 续传，实际 Range 序列 = %v（partial=%d）", secondRanges, partial.Size())
	}
	if want := int64(total) - partial.Size(); secondServed != want {
		t.Errorf("第二段实际下载 %d 字节，期望只补缺口 %d（总量 %d，已存 %d）", secondServed, want, total, partial.Size())
	}
}
