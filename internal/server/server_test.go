package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QC3284/BBDown-go/internal/config"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost":    true,
		"LOCALHOST":    true,
		"127.0.0.1":    true,
		"::1":          true,
		"0.0.0.0":      false, // must NOT be exempt (upstream)
		"::":           false,
		"192.168.1.10": false,
		"example.com":  false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestValidateListenURL(t *testing.T) {
	cases := []struct {
		listen string
		token  string
		wantOK bool
	}{
		{"http://127.0.0.1:23333", "", true},
		{"http://localhost:23333", "", true},
		{"http://[::1]:23333", "", true},
		{"http://0.0.0.0:23333", "", false}, // non-loopback without token: refuse
		{"http://0.0.0.0:23333", "tok", true},
		{"http://192.168.1.5:23333", "", false},
		{"http://[::]:23333", "", false},
		{"https://127.0.0.1:23333", "", false}, // http scheme only
	}
	for _, c := range cases {
		err := validateListenURL(c.listen, c.token)
		if c.wantOK && err != nil {
			t.Errorf("validateListenURL(%q, %q) unexpected error: %v", c.listen, c.token, err)
		}
		if !c.wantOK && err == nil {
			t.Errorf("validateListenURL(%q, %q) should refuse", c.listen, c.token)
		}
	}
}

func TestGenerateJobID(t *testing.T) {
	if len(generateJobID()) != 32 {
		t.Fatal("job id must be 32 hex chars")
	}
}

func TestSegmentHasPrefix(t *testing.T) {
	if !segmentHasPrefix("/get-tasks/abc", "/get-tasks") {
		t.Error("segment prefix should match")
	}
	if segmentHasPrefix("/get-tasksXYZ", "/get-tasks") {
		t.Error("non-segment prefix should not match")
	}
}

func TestIsSafeCallbackURL(t *testing.T) {
	// Stub DNS so the domain branch is deterministic and offline (CI-safe).
	origResolver := dnsLookupIP
	t.Cleanup(func() { dnsLookupIP = origResolver })
	dnsLookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
		switch host {
		case "public.example":
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		case "evil.example":
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		case "mixed.example":
			return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.1")}, nil
		default:
			return nil, fmt.Errorf("no such host")
		}
	}

	// Literal-IP branch (upstream semantics).
	if isSafeCallbackURL("http://127.0.0.1:9999/hook") {
		t.Error("loopback literal should be blocked")
	}
	if isSafeCallbackURL("http://localhost/hook") {
		t.Error("localhost should be blocked")
	}
	if isSafeCallbackURL("http://169.254.169.254/hook") {
		t.Error("cloud metadata literal should be blocked")
	}
	if isSafeCallbackURL("http://0.0.0.0:9999/hook") {
		t.Error("unspecified literal should be blocked")
	}
	if !isSafeCallbackURL("http://192.168.1.5/hook") {
		t.Error("RFC1918 LITERAL is allowed (no DNS rebinding possible)")
	}
	if isSafeCallbackURL("ftp://example.com/hook") {
		t.Error("non-http scheme should be blocked")
	}
	if !isSafeCallbackURL("") {
		t.Error("empty URL means no webhook configured: legal (upstream)")
	}

	// Domain branch (stubbed DNS, deterministic).
	if !isSafeCallbackURL("https://public.example/hook") {
		t.Error("public domain should be allowed")
	}
	if isSafeCallbackURL("https://evil.example/hook") {
		t.Error("domain resolving to private IP should be blocked")
	}
	if isSafeCallbackURL("https://mixed.example/hook") {
		t.Error("any private address among resolved IPs should block the domain")
	}
	if isSafeCallbackURL("https://unresolvable.example/hook") {
		t.Error("DNS failure should block the callback")
	}
}

func TestIsBlockedAddress(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":            true,
		"10.1.2.3":             true,
		"172.16.0.1":           true,
		"172.32.0.1":           false, // outside 172.16/12
		"192.168.1.1":          true,
		"100.64.0.1":           true,
		"100.128.0.1":          false,
		"169.254.1.1":          true,
		"8.8.8.8":              false,
		"fc00::1":              true,
		"2001:4860:4860::8888": false,
	}
	for addr, want := range cases {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("bad test address %q", addr)
		}
		if got := isBlockedAddress(normalizeMappedIP(ip)); got != want {
			t.Errorf("isBlockedAddress(%s) = %v, want %v", addr, got, want)
		}
	}
}

// ---- /add-task 字段白名单（Web UI 表单的后端契约）----
//
// /add-task 此前只解出 url 一个字段；开表单意味着要**显式**放开一批字段。下面这组用例把
// 「字段 → 任务 cfg」的映射、每个字段的值域、以及未知字段的严格模式逐条钉住。
// 全部离线：请求走 buildHandler（含 Host/Origin/Content-Type 门禁），任务用 url "x"
// （ResolveURL 本地就失败，不会联网、不会碰文件系统）。

// addTaskBody 把字段拼成 /add-task 的 JSON 请求体。
func addTaskBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(data)
}

// postAddTask 按真人客户端的方式提交（回环 Host、无 Origin、application/json）。
func postAddTask(s *APIServer, body string) *httptest.ResponseRecorder {
	return do(s.buildHandler(), http.MethodPost, "http://127.0.0.1:23333/add-task",
		"127.0.0.1:23333", "", "application/json", body)
}

// observeAddTaskOption 抓一次 /add-task 构造出的任务配置（观察点只在本用例生存期内装），
// 并等这次请求起出来的任务 goroutine 收尾（没有任务时立即返回）。
func observeAddTaskOption(t *testing.T, s *APIServer, body string) (config.MyOption, *httptest.ResponseRecorder) {
	t.Helper()
	var got config.MyOption
	prev := addTaskOptionObserver
	addTaskOptionObserver = func(o config.MyOption) { got = o }
	t.Cleanup(func() { addTaskOptionObserver = prev })
	rec := postAddTask(s, body)
	waitAddTaskDone(t, s)
	return got, rec
}

// waitAddTaskDone 等任务 goroutine 收尾。202 之后任务是**异步**的：它还会往注入的 taskFile
// （t.TempDir 里）写持久化文件，不等它，t.TempDir 的 RemoveAll 清理就会与写入撞车
// （报 "directory not empty"，机器越忙越容易红），用例也就变成看运气。
// 轮询可观测状态并设上限，不靠固定 Sleep（AGENTS.md 测试纪律）。
func waitAddTaskDone(t *testing.T, s *APIServer) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.taskWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("任务 goroutine 10s 内没有收尾：serve 任务不允许挂在后台跨用例")
	}
}

// addTaskErrorMessage 解出 /add-task 错误体里的 error 文本：错误体本身是 JSON（字段名带引号
// 会被转义），直接对原文做字符串匹配会被转义干扰，容易断错地方。
func addTaskErrorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(rec.Body.String())), &body); err != nil {
		t.Fatalf("错误体不是 JSON 对象: %v (%q)", err, rec.Body.String())
	}
	return body["error"]
}

// addTaskTaskCount 在锁内读一次任务数（被拒的请求用它断言「没有任务被登记」）。
func addTaskTaskCount(s *APIServer) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runningTasks) + len(s.finishedTasks)
}

// wantAbs 是 work_dir 的期望值：与实现同一条绝对化路径（filepath.Abs），避免平台差异。
func wantAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("Abs(%q): %v", p, err)
	}
	return abs
}

// TestAddTaskOldClientConfigUnchanged：只发 url 的老客户端（2.13.0 形态）必须拿到与改前
// **逐字段相同**的任务配置（DefaultMyOption + URL）与 202 响应。
//
// 变异验证：把 addTaskRequest 的 *bool 换成 bool（零值覆盖默认）→ 本用例的
// multi_thread / skip_ai 比较变红。
func TestAddTaskOldClientConfigUnchanged(t *testing.T) {
	s := newLoopbackServer(t)
	got, rec := observeAddTaskOption(t, s, addTaskBody(t, map[string]any{"url": "x"}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("老客户端提交 = %d, want 202", rec.Code)
	}
	want := config.DefaultMyOption()
	want.URL = "x"
	if got != want {
		t.Errorf("老客户端的任务配置必须与改前一致（只有 URL 生效）:\ngot  %+v\nwant %+v", got, want)
	}
	var resp AddTaskResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(rec.Body.String())), &resp); err != nil {
		t.Fatalf("响应体不是 AddTaskResponse: %v (%q)", err, rec.Body.String())
	}
	if len(resp.TaskID) != 32 {
		t.Errorf("响应 TaskId = %q, want 32 位 hex", resp.TaskID)
	}
}

// TestAddTaskWhitelistFieldsMapToTaskConfig：白名单 15 个字段逐个映射到任务 cfg。
// url 之外都给「最容易被漏掉的那一侧」的值（multi_thread/skip_ai 给 false，其余打开），
// 任何一个字段没接上都会红。
func TestAddTaskWhitelistFieldsMapToTaskConfig(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "out")
	s := newLoopbackServer(t)
	body := addTaskBody(t, map[string]any{
		"url":               "x",
		"select_page":       "1-3",
		"dfn_priority":      " 8K 4K 1080P 高码率 ",
		"encoding_priority": "hevc,avc,av1",
		"multi_thread":      false,
		"overwrite":         true,
		"skip_mux":          true,
		"skip_ai":           false,
		"write_nfo":         true,
		"compat":            true,
		"use_app_api":       true,
		"use_tv_api":        true,
		"use_intl_api":      true,
		"work_dir":          workDir,
		"language":          "zh-CN",
	})
	got, rec := observeAddTaskOption(t, s, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("全字段提交 = %d, want 202（body=%s）", rec.Code, rec.Body.String())
	}

	want := config.DefaultMyOption()
	want.URL = "x"
	want.SelectPage = "1-3"
	want.DfnPriority = "8K 4K 1080P 高码率" // 两端空白去掉，内容原样
	want.EncodingPriority = "hevc,avc,av1"
	want.MultiThread = false // 默认 true → 显式 false 必须能关掉
	want.Overwrite = true
	want.SkipMux = true
	want.SkipAi = false // 默认 true → 显式 false 必须能关掉
	want.WriteNFO = true
	want.Compat = true
	want.UseAppAPI = true
	want.UseTvAPI = true
	want.UseIntlAPI = true
	want.WorkDir = wantAbs(t, workDir)
	want.Language = "zh-CN"
	if got != want {
		t.Errorf("字段映射:\ngot  %+v\nwant %+v", got, want)
	}
	// 白名单之外的字段必须保持默认（凭据与回调尤其不能从请求里进来）。
	if got.Cookie != "" || got.AccessToken != "" || got.NotifyWebhook != "" ||
		got.ForceReplaceHost != want.ForceReplaceHost || got.MuxerTimeout != want.MuxerTimeout {
		t.Errorf("白名单之外的字段被改动了: %+v", got)
	}
}

// TestAddTaskRejectsUnknownFields：严格模式——未知顶层字段一律 400 并点名。
// 这些字段各自是一个注入面（见 addTaskAllowedFields 上方的注释），不能「收下但不生效」。
//
// 变异验证：让 unknownAddTaskFields 恒返回 nil → 本用例全部变红（会拿到 202）。
func TestAddTaskRejectsUnknownFields(t *testing.T) {
	s := newLoopbackServer(t)
	for _, field := range []string{
		"cookie", "access_token", "user_agent", "interactive", "file_pattern",
		"multi_file_pattern", "danmaku_filter", "notify_webhook", "insecure",
		"decrypt_drm", "drm_key_hex", "drm_kid_hex", "mp4decrypt_path", "wvd_path",
		"config_file", "area", "retry_count", "debug", "selectPage",
	} {
		body := addTaskBody(t, map[string]any{"url": "x", field: "v"})
		rec := postAddTask(s, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("字段 %q: status %d, want 400（未知字段必须当场报错）", field, rec.Code)
			continue
		}
		msg := addTaskErrorMessage(t, rec)
		if want := fmt.Sprintf("unknown field %q in request body", field); !strings.HasPrefix(msg, want) {
			t.Errorf("字段 %q: 报错要点名字段，实际 %q（want 前缀 %q）", field, msg, want)
		}
		if !json.Valid([]byte(strings.TrimSpace(rec.Body.String()))) {
			t.Errorf("字段 %q: 错误体必须是合法 JSON（点名带引号），实际 %q", field, rec.Body.String())
		}
		if n := addTaskTaskCount(s); n != 0 {
			t.Fatalf("字段 %q 被拒绝后不该登记任务，实际 %d 条", field, n)
		}
	}

	// 多个未知字段：报错稳定（排序后点名，不受 map 迭代顺序影响）。
	rec := postAddTask(s, addTaskBody(t, map[string]any{"url": "x", "zeta": 1, "alpha": 1}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("多个未知字段 = %d, want 400", rec.Code)
	}
	msg := addTaskErrorMessage(t, rec)
	if !strings.HasPrefix(msg, "unknown fields \"alpha\", \"zeta\" in request body") {
		t.Errorf("多个未知字段都要点名且顺序稳定（排序后）: %q", msg)
	}
	if !strings.Contains(msg, "allowed: url, select_page") {
		t.Errorf("报错里应当给出允许的字段清单: %q", msg)
	}
}

// TestAddTaskRejectsInvalidWhitelistValues：白名单字段的值域逐条钉住（非法 → 400 并点名字段）。
//
// 变异验证：删掉 validateAddTaskFields 的任一分支（select_page / 优先级 / work_dir / language）
// → 对应的那一行变红。
func TestAddTaskRejectsInvalidWhitelistValues(t *testing.T) {
	s := newLoopbackServer(t)
	cases := []struct {
		name      string
		fields    map[string]any
		wantField string
	}{
		{"select_page 超服务端上限", map[string]any{"select_page": "1-100000"}, "select_page"},
		{"select_page 非数字", map[string]any{"select_page": "abc"}, "select_page"},
		{"select_page 起始大于结束", map[string]any{"select_page": "10-1"}, "select_page"},
		{"select_page 超解析器累计上限", map[string]any{"select_page": "1-60000,1-60000"}, "select_page"},
		{"dfn_priority 超长", map[string]any{"dfn_priority": strings.Repeat("高", maxPriorityLen+1)}, "dfn_priority"},
		{"dfn_priority 非法字符", map[string]any{"dfn_priority": "1080P;rm -rf /"}, "dfn_priority"},
		{"encoding_priority 含换行", map[string]any{"encoding_priority": "avc\nav1"}, "encoding_priority"},
		{"encoding_priority 含引号", map[string]any{"encoding_priority": "avc\"x"}, "encoding_priority"},
		{"work_dir 超长", map[string]any{"work_dir": "/" + strings.Repeat("d", maxWorkDirLen)}, "work_dir"},
		{"work_dir 含控制字符", map[string]any{"work_dir": "/tmp/\nrm"}, "work_dir"},
		{"language 超长", map[string]any{"language": strings.Repeat("z", maxLanguageLen+1)}, "language"},
		{"language 含空格", map[string]any{"language": "zh CN"}, "language"},
		{"bool 类型不对", map[string]any{"multi_thread": "yes"}, "multi_thread"},
		{"字符串字段类型不对", map[string]any{"select_page": 3}, "select_page"},
	}
	for _, c := range cases {
		fields := map[string]any{"url": "x"}
		for k, v := range c.fields {
			fields[k] = v
		}
		rec := postAddTask(s, addTaskBody(t, fields))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400（body=%s）", c.name, rec.Code, rec.Body.String())
			continue
		}
		msg := addTaskErrorMessage(t, rec)
		if !strings.Contains(msg, c.wantField) {
			t.Errorf("%s: 报错要点名 %q，实际 %q", c.name, c.wantField, msg)
		}
		if !json.Valid([]byte(strings.TrimSpace(rec.Body.String()))) {
			t.Errorf("%s: 错误体必须是合法 JSON，实际 %q", c.name, rec.Body.String())
		}
		if n := addTaskTaskCount(s); n != 0 {
			t.Fatalf("%s 被拒绝后不该登记任务，实际 %d 条", c.name, n)
		}
	}
}

// TestAddTaskLegacyBadRequestBodyUnchanged：老路径的两条 400 文案与 2.13.0 逐字一致
// （解析失败 / 缺 url）。改错误体的编码方式时最容易把这两句一起改掉。
func TestAddTaskLegacyBadRequestBodyUnchanged(t *testing.T) {
	s := newLoopbackServer(t)
	legacy, err := json.Marshal(map[string]string{"error": "invalid request body, 'url' required"})
	if err != nil {
		t.Fatal(err)
	}
	wantBody := string(legacy) + "\n"
	for _, body := range []string{"{", "{}", "[]", "null", addTaskBody(t, map[string]any{"url": ""})} {
		rec := postAddTask(s, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, rec.Code)
			continue
		}
		if rec.Body.String() != wantBody {
			t.Errorf("body %q: 400 文案必须与改前逐字一致\ngot  %q\nwant %q", body, rec.Body.String(), wantBody)
		}
	}
}

// TestAddTaskAcceptsBoundaryValues：值域上限是**闭区间**（恰好等于上限要放行），
// 免得守卫写成 >= 把合法输入也拒掉。
func TestAddTaskAcceptsBoundaryValues(t *testing.T) {
	// work_dir 用临时目录里的一条长路径凑满 512 字节，避免真的往 / 写东西。
	base := t.TempDir()
	padLen := maxWorkDirLen - len(base) - 1
	if padLen < 1 {
		t.Fatalf("临时目录太长，无法拼出 %d 字节的路径: %s", maxWorkDirLen, base)
	}
	boundary := filepath.Join(base, strings.Repeat("d", padLen))
	if len(boundary) != maxWorkDirLen {
		t.Fatalf("拼出的路径长度 %d, want %d", len(boundary), maxWorkDirLen)
	}
	cases := []struct {
		name   string
		fields map[string]any
		check  func(t *testing.T, cfg config.MyOption) error
	}{
		{
			"select_page 恰好等于服务端上限",
			map[string]any{"select_page": fmt.Sprintf("1-%d", maxSelectPageItems)},
			func(_ *testing.T, cfg config.MyOption) error {
				if want := fmt.Sprintf("1-%d", maxSelectPageItems); cfg.SelectPage != want {
					return fmt.Errorf("SelectPage = %q, want %q", cfg.SelectPage, want)
				}
				return nil
			},
		},
		{
			"dfn_priority 恰好等于长度上限",
			map[string]any{"dfn_priority": strings.Repeat("高", maxPriorityLen)},
			func(_ *testing.T, cfg config.MyOption) error {
				if n := len([]rune(cfg.DfnPriority)); n != maxPriorityLen {
					return fmt.Errorf("长度 = %d, want %d", n, maxPriorityLen)
				}
				return nil
			},
		},
		{
			"work_dir 恰好等于长度上限",
			map[string]any{"work_dir": boundary},
			func(t *testing.T, cfg config.MyOption) error {
				if want := wantAbs(t, boundary); cfg.WorkDir != want {
					return fmt.Errorf("WorkDir = %q, want %q", cfg.WorkDir, want)
				}
				return nil
			},
		},
		{
			"language 恰好等于长度上限",
			map[string]any{"language": strings.Repeat("z", maxLanguageLen)},
			func(_ *testing.T, cfg config.MyOption) error {
				if want := strings.Repeat("z", maxLanguageLen); cfg.Language != want {
					return fmt.Errorf("Language = %q, want %q", cfg.Language, want)
				}
				return nil
			},
		},
	}
	for _, c := range cases {
		// 每个子用例一台新服务器：202 会真的起一个任务 goroutine（url "x" 本地解析失败）。
		s := newLoopbackServer(t)
		fields := map[string]any{"url": "x"}
		for k, v := range c.fields {
			fields[k] = v
		}
		got, rec := observeAddTaskOption(t, s, addTaskBody(t, fields))
		if rec.Code != http.StatusAccepted {
			t.Errorf("%s: status %d, want 202（body=%s）", c.name, rec.Code, rec.Body.String())
			continue
		}
		if err := c.check(t, got); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
