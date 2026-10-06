package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/QC3284/BBDown-go/internal/download"
)

// 任务 T ②：/get-tasks 的字节进度与 SSE **同口径**（口径分工声明见 progress.go 顶部
// 「两个进度口径」与 DownloadTask 的字段注释）。
//
// 契约（本文件逐条钉住）：
//
//   - SSE 帧（GET /events 的 task_progress）：**逐文件**真实字节进度。percent 0~100、
//     downloaded/total 为整份文件口径（含续传 base），单一来源是下载层观察者；
//   - /get-tasks 的 TotalDownloadedBytes：**任务级字节数** = max(按文件身份累计的实时字节,
//     已落盘产物字节)。下载执行期间与 SSE 同口径——单产物任务就等于最近一帧；多产物任务
//     是各身份最近一帧之和（文件切换不回跳）；没有观察者的路径（aria2c）等于产物累计；
//   - /get-tasks 的 Progress：仍是**边界语义**（执行期间 0、成功 1.0）。
//
// 口径变更理由（相对上一版契约，逐条写明；一次断言都没有删，只换了判据）：
//
//  1. 上一版要求「观察者的字节不得回写任务字段」，理由是「ProgressEvent 没有文件身份，
//     多产物任务在文件切换时回跳」。本次给 ProgressEvent 加了 Key（任务内文件身份，见
//     internal/download/progressobserver.go 与 progressObserverFor），回跳的理由消失：
//     服务端按身份存映射、求和，A 报完切到 B 时 A 的值留在映射里。
//  2. 上一版的另一半理由是「aria2c 路径没有观察者，回写会把进度永久钉在 0」。这条**仍然
//     成立**，所以没有改成「只取实时字节」，而是取「实时映射 / 产物字节」的较大者
//     ——映射为空时它逐字等于产物累计，aria2c 语义不变（第 ④ 段钉住）。
//  3. Progress 的兼容面（bbdown-tasks.json / 上游 0~1 契约）不受影响：它仍只在成功时
//     置 1.0（第 ⑤ 段钉住）。本次只统一**字节**口径，不动 Progress。
//
// 取帧方式：直接订阅事件总线（s.events.subscribe）后非阻塞排空队列，不经过真实 SSE socket
// ——「事件经 hub 写到 /events 客户端」这条传输链由 TestDownloadProgressReachesSSEClient 覆盖；
// 本文件钉的是**数字口径**，用总线可以让「缺帧」立刻表现为断言红，而不是读 socket 的
// deadline 超时（失败形态必须干净，见 AGENTS「断言失败前先释放资源」）。
// /get-tasks 走**回环 harness**（s.buildHandler() + httptest：无 socket、无网络、无真实下载）。
//
// 变异验证（撤掉任一侧都必须红）：
//   - 去掉 publishDownloadProgress 里的 recordProgressBytesLocked 接线（字节映射接线）→
//     第 ②/③ 段的 TotalDownloadedBytes 断言红（退回 0 / 只剩产物累计）；
//   - 把 totalBytesLocked 的「取较大者」改成只取实时映射 → 第 ④ 段红；
//   - 让 SSE 帧改读任务字段（把逐文件帧降级成任务级边界值）→ 第 ③ 段红
//     （那一帧必须只报 audio 自己的 512KiB，而任务级是 2.5MiB）；
//   - 改 DownloadTask 的 JSON tag（或去掉字段）→ 第 ⑤ 段的线格式断言红；
//   - 把 progressBytes / artifactBytes 改成导出字段（无 tag 就会被序列化）→ 第 ⑥ 段红。
func TestProgressAccountsContract(t *testing.T) {
	s := newLoopbackServer(t)
	// 节流在本用例里关掉：这里钉的是**数字口径**，帧数上限由
	// TestDownloadProgressPublishThrottle 覆盖；开着节流会把「每喂一帧就比对」变成时序题。
	s.progressPublishInterval = 0

	client, ok := s.events.subscribe()
	if !ok {
		t.Fatal("订阅事件总线失败")
	}
	defer s.events.unsubscribe(client)

	task := newTestTask("job-account")
	task.SetStatus(StatusRunning)
	s.mu.Lock()
	s.runningTasks = append(s.runningTasks, task)
	s.mu.Unlock()

	// 观察者接线点：与真实任务同一条路径（processTask → taskDownloadContext → workflow.Run）。
	fn := download.ProgressObserverFromContext(s.taskDownloadContext(context.Background(), task))
	if fn == nil {
		t.Fatal("任务 ctx 上没有装进度观察者：SSE 收不到真实字节进度（接线被去掉了？）")
	}

	// 回环 harness：/get-tasks 走真实 router（认证 / 限流 / 序列化都在），只是不起 socket。
	getTask := func(id string) progressWireTask {
		t.Helper()
		rec := do(s.buildHandler(), http.MethodGet, "http://127.0.0.1:23333/get-tasks/"+id,
			"127.0.0.1:23333", "", "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /get-tasks/%s = %d, want 200: %s", id, rec.Code, rec.Body.String())
		}
		var got progressWireTask
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("/get-tasks/%s 不是合法 JSON: %v: %s", id, err, rec.Body.String())
		}
		return got
	}

	// drainFrames 非阻塞排空事件总线，返回最后一条属于 id 的帧。
	// 发布路径不做 IO、不等客户端，所以「喂一帧 → 排空」之间不需要等待/轮询。
	drainFrames := func(id string) (ServerEvent, bool) {
		last, seen := ServerEvent{}, false
		for {
			select {
			case ev := <-client.ch:
				if ev.JobID == id {
					last, seen = ev, true
				}
				continue
			default:
			}
			return last, seen
		}
	}

	// ① SSE 帧是**逐文件真实字节进度**：percent 0~100、downloaded/total 为整份文件口径
	//    （含续传 base：这里 Current 已是 base+本次传输），与 --progress-json 逐字段同义。
	fn(download.ProgressEvent{
		Key: progressVideoKey, Current: 3 * progressMiB / 2, Total: 2 * progressMiB, SpeedBps: progressMiB,
	})
	byteFrame, ok := drainFrames("job-account")
	if !ok {
		t.Fatal("没有收到观察者发出的逐字节进度帧：SSE 上只有边界值")
	}
	if byteFrame.Type != EventTaskProgress || byteFrame.State != StateProgress || byteFrame.Status != StatusRunning {
		t.Errorf("字节帧的类型/状态不符: %+v", byteFrame)
	}
	if byteFrame.Percent != 75 || byteFrame.Downloaded != 3*progressMiB/2 ||
		byteFrame.Total != 2*progressMiB || byteFrame.Speed != progressMiB {
		t.Errorf("SSE 帧必须是观察者的真实字节进度：percent=%v downloaded=%d total=%d speed=%v，want 75/%d/%d/%d",
			byteFrame.Percent, byteFrame.Downloaded, byteFrame.Total, byteFrame.Speed,
			3*progressMiB/2, 2*progressMiB, progressMiB)
	}

	// ② 本次变更的核心（回环 harness 断言）：**单产物任务**里 /get-tasks 的
	//    TotalDownloadedBytes 必须等于最近一次 SSE task_progress 帧的 downloaded
	//    ——含续传 base 的实时字节，而不是旧口径的「执行期间保持 0 / 只随产物落盘累加」。
	running := getTask("job-account")
	if running.Status != StatusRunning {
		t.Errorf("状态字段不符：%q，want %q", running.Status, StatusRunning)
	}
	if running.TotalDownloadedBytes != byteFrame.Downloaded {
		t.Errorf("TotalDownloadedBytes = %d, want %d（必须与最近一帧 SSE 的 downloaded 同口径）",
			running.TotalDownloadedBytes, byteFrame.Downloaded)
	}
	// Progress 仍是**边界语义**：执行期间 0（实时字节比例只在 SSE）。这条没有随本次变更而改。
	if running.Progress != 0 {
		t.Errorf("执行期间 Progress 必须是边界值 0（实时字节进度只在 SSE），实际 %v", running.Progress)
	}

	// 续传末帧（base + 本次剩余 = 整份文件）同样要落到任务级字段上：每喂一帧都比对一次。
	fn(download.ProgressEvent{
		Key: progressVideoKey, Current: 2 * progressMiB, Total: 2 * progressMiB, SpeedBps: progressMiB,
	})
	lastFrame, ok := drainFrames("job-account")
	if !ok {
		t.Fatal("没有收到同一文件（同一身份）的末帧")
	}
	if got := getTask("job-account").TotalDownloadedBytes; got != lastFrame.Downloaded || got != 2*progressMiB {
		t.Errorf("末帧后 TotalDownloadedBytes = %d, want %d（= 最近一帧 downloaded = 2MiB）",
			got, lastFrame.Downloaded)
	}

	// ③ 多产物任务：任务级数字 = 各**文件身份**最近一帧之和，切文件不回跳——这正是本次给
	//    ProgressEvent 加文件身份的目的。注意 SSE 帧仍是**逐文件**口径（这一帧报的是 audio
	//    自己的 512KiB），所以本段刻意**不**断言两者相等；把 SSE 降级成任务字段会让本段红。
	fn(download.ProgressEvent{
		Key: progressAudioKey, Current: progressMiB / 2, Total: progressMiB, SpeedBps: 0,
	})
	audioFrame, ok := drainFrames("job-account")
	if !ok {
		t.Fatal("没有收到第二个文件的进度帧")
	}
	if audioFrame.Downloaded != progressMiB/2 {
		t.Errorf("第二个文件的 SSE 帧 downloaded = %d, want %d（SSE 是逐文件口径）",
			audioFrame.Downloaded, progressMiB/2)
	}
	const wantTaskBytes int64 = 2*progressMiB + progressMiB/2
	if got := getTask("job-account").TotalDownloadedBytes; got != wantTaskBytes {
		t.Errorf("多产物任务 TotalDownloadedBytes = %d, want %d（= 各身份最近一帧之和，切文件不回跳）",
			got, wantTaskBytes)
	}

	// ④ 没有观察者的路径（aria2c，§4.57：黑盒，只有开始/结束）保持既有边界语义：
	//    唯一信号是产物落盘，任务级字节数就是产物累计（实时映射为空）。
	silent := newTestTask("job-aria2c")
	silent.SetStatus(StatusRunning)
	s.mu.Lock()
	s.runningTasks = append(s.runningTasks, silent)
	s.mu.Unlock()

	artifact := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(artifact, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	s.onArtifactSaved(silent, artifact)
	s.onArtifactSaved(silent, artifact) // 重试/续传的重复上报：只计一次
	if got := getTask("job-aria2c").TotalDownloadedBytes; got != 2048 {
		t.Errorf("无观察者路径 TotalDownloadedBytes = %d, want 2048（产物累计 = 唯一进度信号，且不重复计数）", got)
	}

	// ⑤ 终态仍是边界值：成功时 Progress=1.0、字节数不回退（产物落盘不会顶掉实时映射，
	//    两个来源取较大者）。线格式用**显式 tag 的独立结构**解码（progressWireTask，
	//    不复用 DownloadTask），键名另在下面按原始 JSON 逐个核对。
	task.SetStatus(StatusSucceeded)
	task.mu.Lock()
	task.Progress = 1.0
	task.mu.Unlock()

	done := getTask("job-account")
	if done.Progress != 1.0 {
		t.Errorf("成功后 Progress = %v, want 1.0（终态边界值）", done.Progress)
	}
	if done.TotalDownloadedBytes != wantTaskBytes {
		t.Errorf("成功后 TotalDownloadedBytes = %d, want %d（终态不回退）",
			done.TotalDownloadedBytes, wantTaskBytes)
	}

	// 线格式：字段名必须是**精确**的这几个键。只靠结构体解码抓不到 tag 被改（json.Unmarshal
	// 对字段名大小写不敏感：`json:"JobId"` 改成 `json:"JobID"` 照样能解出来，那是假绿），
	// 所以这里查原始键名。
	detailRec := do(s.buildHandler(), http.MethodGet, "http://127.0.0.1:23333/get-tasks/job-account",
		"127.0.0.1:23333", "", "", "")
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET /get-tasks/job-account = %d, want 200", detailRec.Code)
	}
	var rawKeys map[string]json.RawMessage
	if err := json.Unmarshal(detailRec.Body.Bytes(), &rawKeys); err != nil {
		t.Fatalf("任务详情不是合法 JSON 对象: %v: %s", err, detailRec.Body.String())
	}
	for _, key := range []string{"JobId", "Progress", "TotalDownloadedBytes", "Status"} {
		if _, ok := rawKeys[key]; !ok {
			t.Errorf("线格式缺少字段 %q（JSON tag 被改动了？）：%s", key, detailRec.Body.String())
		}
	}

	// ⑥ bbdown-tasks.json 兼容：持久化记录与 /get-tasks 序列化的是**同一个 DownloadTask**。
	//    本次新增的实时映射与产物累计都是未导出字段（progressBytes / artifactBytes），
	//    不得出现在 JSON 里——键集必须与旧契约逐字一致（多一个键就是状态文件格式变更，
	//    上游/旧版读它的人会看到不认识的字段）。这里刻意用**全量键集**断言：
	//    以后谁把内部字段改成导出（或加一个没写清语义的导出字段）都会在这里变红。
	persisted, err := json.Marshal(task.Snapshot())
	if err != nil {
		t.Fatalf("序列化任务快照失败: %v", err)
	}
	var persistedKeys map[string]json.RawMessage
	if err := json.Unmarshal(persisted, &persistedKeys); err != nil {
		t.Fatalf("任务快照不是合法 JSON 对象: %v: %s", err, persisted)
	}
	wantKeys := map[string]bool{
		"JobId": true, "Aid": true, "Url": true, "TaskCreateTime": true,
		"Progress": true, "TotalDownloadedBytes": true, "IsSuccessful": true, "Status": true,
	}
	for k := range wantKeys {
		if _, ok := persistedKeys[k]; !ok {
			t.Errorf("持久化记录缺少字段 %q（bbdown-tasks.json 契约被改动了？）：%s", k, persisted)
		}
	}
	for k := range persistedKeys {
		if !wantKeys[k] {
			t.Errorf("持久化记录多出字段 %q（内部状态泄漏进 bbdown-tasks.json 契约？）：%s", k, persisted)
		}
	}
	if got := done.TotalDownloadedBytes; got != wantTaskBytes {
		t.Errorf("持久化前的任务级字节数 = %d, want %d", got, wantTaskBytes)
	}

	// 任务清单（/get-tasks/running 返回**裸数组**，TaskListResponse 只用于根路径）里必须是
	// 同一个数字：轮询客户端最常打的接口不能与服务端任务级字段有第二种口径。
	rec := do(s.buildHandler(), http.MethodGet, "http://127.0.0.1:23333/get-tasks/running",
		"127.0.0.1:23333", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /get-tasks/running = %d, want 200", rec.Code)
	}
	var list []progressWireTask
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("/get-tasks/running 不是合法 JSON: %v: %s", err, rec.Body.String())
	}
	var listed *progressWireTask
	for i := range list {
		if list[i].JobID == "job-account" {
			listed = &list[i]
			break
		}
	}
	if listed == nil {
		t.Fatalf("/get-tasks/running 里找不到任务 job-account：%s", rec.Body.String())
	}
	if listed.TotalDownloadedBytes != wantTaskBytes {
		t.Errorf("清单里的 TotalDownloadedBytes = %d, want %d（与任务详情同口径）",
			listed.TotalDownloadedBytes, wantTaskBytes)
	}
}

// progressWireTask 是 /get-tasks 的**线格式**（显式 tag，不复用 DownloadTask）：
// JSON tag 被改动或字段被去掉时，解码不出这些字段，用例变红。
type progressWireTask struct {
	JobID                string     `json:"JobId"`
	Progress             float64    `json:"Progress"`
	TotalDownloadedBytes int64      `json:"TotalDownloadedBytes"`
	Status               TaskStatus `json:"Status"`
}

// 两个产物身份（真实运行时是产物路径，见 download.progressObserverFor）：同一任务内
// 不同文件必然不同，同一文件在续传/重试里不变。
const (
	progressVideoKey = "/out/video.mp4"
	progressAudioKey = "/out/audio.m4a"
)
