package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/QC3284/BBDown/internal/download"
)

// 任务 T ②：/get-tasks 与 SSE 的**数字口径契约**（两个口径有意不同，声明见 progress.go 顶部
// 「两个进度口径」与 DownloadTask 的 Progress / TotalDownloadedBytes 字段注释）。
//
// 契约（本文件逐条钉住）：
//
//   - SSE 帧（GET /events 的 task_progress）：真实字节进度。percent 0~100、downloaded/total
//     为整份文件口径（含续传 base），单一来源是下载层观察者（download.ProgressEvent）；
//   - /get-tasks 的 Progress / TotalDownloadedBytes：服务端边界值。Progress 执行期间保持 0、
//     只在成功时 1.0；TotalDownloadedBytes 只随产物落盘累加——观察者的字节**不得**回写这两个字段。
//
// 为什么不做成同一个数字：见 progress.go（观察者没有文件身份、多产物任务会回跳；
// aria2c 路径没有观察者，§4.57；/get-tasks 与 bbdown-tasks.json 是上游兼容契约）。
//
// 取帧方式：直接订阅事件总线（s.events.subscribe）后非阻塞排空队列，不经过真实 SSE socket——
// 「事件经 hub 写到 /events 客户端」这条传输链由 TestDownloadProgressReachesSSEClient 覆盖；
// 本文件钉的是**数字口径**，用总线可以让「缺帧」立刻表现为断言红，而不是读 socket 的
// deadline 超时（失败形态必须干净，见 AGENTS「断言失败前先释放资源」）。
//
// 变异验证（撤掉任一侧的口径都必须红）：
//   - 在 publishDownloadProgress 里把观察者最新值回写任务字段（一致性方案①的朴素实现）→
//     第 ②/④ 段断言红；
//   - 让 SSE 的字节帧改用任务字段（等于把 SSE 降级成边界值）→ 第 ① 段断言红；
//   - 改 DownloadTask 的 JSON tag（或去掉字段）→ 第 ③ 段的线格式断言红。
func TestProgressAccountsContract(t *testing.T) {
	s := newLoopbackServer(t)

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

	// 边界信号：一件 2048 字节的产物落盘（服务端唯一能看到的进度边界）。
	artifact := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(artifact, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	s.onArtifactSaved(task, artifact)

	// 真实字节信号：观察者喂一帧 1.5 MiB / 2 MiB。
	fn := download.ProgressObserverFromContext(s.taskDownloadContext(context.Background(), task))
	if fn == nil {
		t.Fatal("任务 ctx 上没有装进度观察者：SSE 收不到真实字节进度（接线被去掉了？）")
	}
	fn(download.ProgressEvent{Current: 3 * progressMiB / 2, Total: 2 * progressMiB, SpeedBps: progressMiB})

	// 非阻塞排空：发布路径不做 IO、不等客户端，所以这里不需要等待/轮询。
	var frames []ServerEvent
	for {
		select {
		case ev := <-client.ch:
			frames = append(frames, ev)
			continue
		default:
		}
		break
	}

	var byteFrame, artifactFrame *ServerEvent
	for i := range frames {
		ev := frames[i]
		if ev.JobID != "job-account" {
			continue
		}
		switch ev.Downloaded {
		case 3 * progressMiB / 2:
			byteFrame = &frames[i]
		case 2048:
			artifactFrame = &frames[i]
		}
	}

	// ① SSE 必须是真实字节进度（不是任务字段的边界值）。
	if byteFrame == nil {
		t.Fatalf("没有收到观察者发出的逐字节进度帧：SSE 上只有边界值（收到 %d 帧）", len(frames))
	}
	if byteFrame.Type != EventTaskProgress || byteFrame.State != StateProgress || byteFrame.Status != StatusRunning {
		t.Errorf("字节帧的类型/状态不符: %+v", *byteFrame)
	}
	if byteFrame.Percent != 75 || byteFrame.Downloaded != 3*progressMiB/2 || byteFrame.Total != 2*progressMiB {
		t.Errorf("SSE 帧必须是观察者的真实字节进度：percent=%v downloaded=%d total=%d，want 75/%d/%d",
			byteFrame.Percent, byteFrame.Downloaded, byteFrame.Total, 3*progressMiB/2, 2*progressMiB)
	}
	if artifactFrame == nil {
		t.Errorf("没有收到产物落盘的边界帧（既有事件语义不得被字节进度顶掉）")
	}

	// ② /get-tasks 仍是服务端边界值：观察者的字节不得回写任务字段。
	//    线格式用**显式 tag 的匿名结构**解码（不复用 DownloadTask），这样改 JSON tag 也会红。
	type wireTask struct {
		JobID                string     `json:"JobId"`
		Progress             float64    `json:"Progress"`
		TotalDownloadedBytes int64      `json:"TotalDownloadedBytes"`
		Status               TaskStatus `json:"Status"`
	}

	rec := do(s.buildHandler(), http.MethodGet, "http://127.0.0.1:23333/get-tasks/running", "127.0.0.1:23333", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /get-tasks/running = %d, want 200", rec.Code)
	}
	// /get-tasks/running 返回裸数组（TaskListResponse 只用于 /get-tasks 根路径）。
	var list []wireTask
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("/get-tasks/running 不是合法 JSON: %v: %s", err, rec.Body.String())
	}
	var running *wireTask
	for i := range list {
		if list[i].JobID == "job-account" {
			running = &list[i]
			break
		}
	}
	if running == nil {
		t.Fatalf("/get-tasks/running 里找不到任务 job-account：%s", rec.Body.String())
	}
	if running.Status != StatusRunning {
		t.Errorf("状态字段不符：%q，want %q", running.Status, StatusRunning)
	}
	if running.Progress != 0 {
		t.Errorf("执行期间 Progress 必须是服务端边界值 0（真实字节进度只在 SSE），实际 %v", running.Progress)
	}
	if running.TotalDownloadedBytes != 2048 {
		t.Errorf("TotalDownloadedBytes 必须仍是已落盘产物字节 2048（观察者的 %d 不得回写），实际 %d",
			3*progressMiB/2, running.TotalDownloadedBytes)
	}

	// ③ 终态仍是边界值：成功时 Progress=1.0、字节数保持产物累计（走 /get-tasks/{id}）。
	task.SetStatus(StatusSucceeded)
	task.mu.Lock()
	task.Progress = 1.0
	task.mu.Unlock()

	rec = do(s.buildHandler(), http.MethodGet, "http://127.0.0.1:23333/get-tasks/job-account", "127.0.0.1:23333", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /get-tasks/job-account = %d, want 200", rec.Code)
	}
	var done wireTask
	if err := json.Unmarshal(rec.Body.Bytes(), &done); err != nil {
		t.Fatalf("任务详情不是合法 JSON: %v: %s", err, rec.Body.String())
	}
	if done.Progress != 1.0 {
		t.Errorf("成功后 Progress = %v, want 1.0（终态边界值）", done.Progress)
	}
	if done.TotalDownloadedBytes != 2048 {
		t.Errorf("成功后 TotalDownloadedBytes = %d, want 2048（产物累计，不被字节进度顶替）", done.TotalDownloadedBytes)
	}
}
