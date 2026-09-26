package server

import (
	"context"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QC3284/BBDown/internal/download"
)

// 任务 Q 的回归网：逐字节进度观察者（download.ProgressEvent）→ 既有 SSE 事件。
//
// 覆盖三段：
//  1. 映射纯函数 progressServerEvent 的整张表（Total=0 / Current>Total / 续传 base / 防御）；
//  2. 接线：任务 ctx 上真的装了观察者，事件经 hub 到达真实 SSE 客户端；
//  3. 节流：同一任务短时间内大量事件 → 客户端收到的帧数有上限，且不阻塞发布者、
//     不把 hub 的队列打爆、不永久静音、不影响其它任务。

const progressMiB = 1 << 20

// progressBase 是映射用例的任务身份帧（publishDownloadProgress 用同一批字段构造它）。
func progressBase() ServerEvent {
	return ServerEvent{
		JobID:  "job-map",
		Aid:    "BV1xx411c7mD",
		URL:    "https://www.bilibili.com/video/BV1xx411c7mD",
		Title:  "标题",
		Status: StatusRunning,
	}
}

// 映射表：percent 是 0~100，downloaded/total 是整份文件口径（含续传 base），
// speed 是观察者的 1 秒窗口速率。口径与 --progress-json 逐字段同义。
func TestProgressServerEventMapping(t *testing.T) {
	cases := []struct {
		name      string
		ev        download.ProgressEvent
		wantPct   float64
		wantDown  int64
		wantTotal int64
		wantSpeed float64
	}{
		{
			name:    "Total=0（长度未知）：percent 保持 0，不估算",
			ev:      download.ProgressEvent{Current: 0, Total: 0, SpeedBps: 0},
			wantPct: 0, wantDown: 0, wantTotal: 0, wantSpeed: 0,
		},
		{
			name:    "Total=0 但已有字节：percent 仍是 0，total 仍是 0",
			ev:      download.ProgressEvent{Current: 4096, Total: 0, SpeedBps: 512},
			wantPct: 0, wantDown: 4096, wantTotal: 0, wantSpeed: 512,
		},
		{
			name:    "常规比例：percent = downloaded/total*100",
			ev:      download.ProgressEvent{Current: 512, Total: 2048, SpeedBps: 1024},
			wantPct: 25, wantDown: 512, wantTotal: 2048, wantSpeed: 1024,
		},
		{
			name: "续传 base：观察者给的就是整份文件口径，映射不再加减 base",
			ev: download.ProgressEvent{
				Current:  progressMiB + progressMiB/2, // 磁盘上 1MiB + 本次传输 0.5MiB
				Total:    2 * progressMiB,             // 整份文件 2MiB（base+本次声明长度）
				SpeedBps: progressMiB,
			},
			wantPct: 75, wantDown: progressMiB + progressMiB/2, wantTotal: 2 * progressMiB, wantSpeed: progressMiB,
		},
		{
			name:    "Current>Total：percent 夹到 100，downloaded 原样（与 --progress-json 一致，不掩盖计数错误）",
			ev:      download.ProgressEvent{Current: 3 * progressMiB, Total: 2 * progressMiB, SpeedBps: 0},
			wantPct: 100, wantDown: 3 * progressMiB, wantTotal: 2 * progressMiB, wantSpeed: 0,
		},
		{
			name:    "还没结算出速率：speed 保持 0",
			ev:      download.ProgressEvent{Current: 1, Total: 10, SpeedBps: 0},
			wantPct: 10, wantDown: 1, wantTotal: 10, wantSpeed: 0,
		},
		{
			name:    "防御：负字节归 0、NaN 速率归 0（否则 json.Marshal 会让整帧失败）",
			ev:      download.ProgressEvent{Current: -5, Total: -1, SpeedBps: math.NaN()},
			wantPct: 0, wantDown: 0, wantTotal: 0, wantSpeed: 0,
		},
		{
			name:    "防御：+Inf 速率归 0",
			ev:      download.ProgressEvent{Current: 5, Total: 10, SpeedBps: math.Inf(1)},
			wantPct: 50, wantDown: 5, wantTotal: 10, wantSpeed: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := progressServerEvent(progressBase(), tc.ev)

			if got.Type != EventTaskProgress || got.State != StateProgress {
				t.Errorf("type/state = %q/%q, want %q/%q", got.Type, got.State, EventTaskProgress, StateProgress)
			}
			if got.Percent != tc.wantPct {
				t.Errorf("percent = %v, want %v（0~100 口径）", got.Percent, tc.wantPct)
			}
			if got.Downloaded != tc.wantDown {
				t.Errorf("downloaded = %d, want %d", got.Downloaded, tc.wantDown)
			}
			if got.Total != tc.wantTotal {
				t.Errorf("total = %d, want %d", got.Total, tc.wantTotal)
			}
			if got.Speed != tc.wantSpeed {
				t.Errorf("speed = %v, want %v", got.Speed, tc.wantSpeed)
			}

			// 身份字段必须原样带出：页面按 job_id 整体替换最新一条事件，掉字段会把标题抹掉。
			base := progressBase()
			if got.JobID != base.JobID || got.Aid != base.Aid || got.URL != base.URL ||
				got.Title != base.Title || got.Status != base.Status {
				t.Errorf("身份字段被映射改动了: %+v", got)
			}

			// 映射结果必须能被编码成 SSE 帧（NaN 一类的值会在这里露出来）。
			if _, err := got.frame(); err != nil {
				t.Errorf("映射出的事件无法编码成 SSE 帧: %v", err)
			}
		})
	}
}

// 接线用例：任务 ctx 上装的观察者发出的事件，必须经 hub 到达真实 SSE 客户端。
//
// 不跑真下载（真下载要联网）：接线点 APIServer.taskDownloadContext 是可离线驱动的——
// 用例取出它挂上去的观察者自己喂一帧，走的仍是「观察者 → publishDownloadProgress → hub →
// /events 客户端」这条生产路径。
//
// 变异验证：把 taskDownloadContext 改成直接返回 ctx（去掉 WithProgressObserver 接线）→
// 观察者为 nil，本用例红；把 progressServerEvent 的 percent 改成 0~1 口径 → 断言红。
func TestDownloadProgressReachesSSEClient(t *testing.T) {
	s := newLoopbackServer(t)
	srv := startEventServer(t, s.buildHandler())

	resp, br, _ := openEventStream(t, eventWaitTimeout, srv.URL+"/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events = %d, want 200", resp.StatusCode)
	}
	if _, err := readSSEFrame(br); err != nil { // hello = 订阅已注册的同步点
		t.Fatalf("握手帧读取失败: %v", err)
	}

	task := newTestTask("job-progress")
	task.mu.Lock()
	task.Title = "逐字节进度"
	task.Status = StatusRunning
	task.mu.Unlock()

	fn := download.ProgressObserverFromContext(s.taskDownloadContext(context.Background(), task))
	if fn == nil {
		t.Fatal("任务 ctx 上没有装进度观察者：serve 收不到任何逐字节进度（接线被去掉了？）")
	}
	fn(download.ProgressEvent{Current: 3 * progressMiB / 2, Total: 2 * progressMiB, SpeedBps: progressMiB})

	var got ServerEvent
	for i := 0; i < 4; i++ { // 上限：最多读 4 帧
		frame, err := readSSEFrame(br)
		if err != nil {
			t.Fatalf("第 %d 帧读取失败: %v", i+1, err)
		}
		ev := decodeFrame(t, frame)
		if ev.JobID == "job-progress" {
			got = ev
			break
		}
	}
	if got.JobID != "job-progress" {
		t.Fatalf("客户端没有收到观察者发出的逐字节进度帧")
	}
	if got.Type != EventTaskProgress || got.State != StateProgress || got.Status != StatusRunning {
		t.Errorf("事件类型/状态不符: %+v", got)
	}
	if got.Percent != 75 || got.Downloaded != 3*progressMiB/2 || got.Total != 2*progressMiB || got.Speed != progressMiB {
		t.Errorf("进度字段不符: percent=%v downloaded=%d total=%d speed=%v", got.Percent, got.Downloaded, got.Total, got.Speed)
	}
	if got.Title != "逐字节进度" {
		t.Errorf("title = %q, want 逐字节进度（身份字段要随新帧带出）", got.Title)
	}
	if got.Seq == 0 || got.Time == 0 {
		t.Errorf("seq/time 未填充: %+v", got)
	}

	// 既有事件仍然是独立的一帧（新增的字节进度不顶掉它们）：产物落盘照旧按既有语义发
	// downloaded=产物字节数、percent 仍是任务快照的 Progress（未改动的旧口径）。
	artifact := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(artifact, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	s.onArtifactSaved(task, artifact)

	var artifactEvent ServerEvent
	for i := 0; i < 4; i++ {
		frame, err := readSSEFrame(br)
		if err != nil {
			t.Fatalf("产物帧读取失败: %v", err)
		}
		ev := decodeFrame(t, frame)
		if ev.JobID == "job-progress" && ev.Type == EventTaskProgress {
			artifactEvent = ev
			break
		}
	}
	if artifactEvent.Downloaded != 2048 {
		t.Errorf("产物帧的 downloaded = %d, want 2048（既有语义不变）", artifactEvent.Downloaded)
	}
}

// 节流上限：同一任务短时间内灌入大量观察者事件，客户端收到的帧数有上限（不是每帧一个）。
//
// 判据分四段，都是行为：
//  1. 上限放大到 1 小时 → 500 帧只放行 1 帧，且发布协程不被阻塞（500 次调用远快于超时）；
//  2. 另一个任务不受影响（节流是 per-task，不是全局闸门）；
//  3. 上限调零后同一任务立刻又能发（是限速，不是一次性闩锁，任务不会被永久静音）；
//  4. 上限关掉后灌 8 倍队列深度的事件：发布仍不阻塞，客户端队列封顶在 hub 的 64 帧
//     （「队列满丢帧」，观察者路径也不会把队列打爆）。
//
// 变异验证：去掉 publishDownloadProgress 里的节流判断 → 第 1 段收到的帧数变成 500，红。
func TestDownloadProgressPublishThrottle(t *testing.T) {
	const burst = 500

	feedOne := func(t *testing.T, s *APIServer, task *DownloadTask, current, total int64) {
		t.Helper()
		fn := download.ProgressObserverFromContext(s.taskDownloadContext(context.Background(), task))
		if fn == nil {
			t.Fatal("任务 ctx 上没有装进度观察者")
		}
		fn(download.ProgressEvent{Current: current, Total: total, SpeedBps: progressMiB})
	}

	s := newLoopbackServer(t)
	s.progressPublishInterval = time.Hour // 上限放到「整个用例期间只允许一帧」
	client, ok := s.events.subscribe()
	if !ok {
		t.Fatal("订阅失败")
	}
	defer s.events.unsubscribe(client)

	task := newTestTask("job-throttle")
	fn := download.ProgressObserverFromContext(s.taskDownloadContext(context.Background(), task))
	if fn == nil {
		t.Fatal("任务 ctx 上没有装进度观察者")
	}

	blocked := make(chan struct{})
	go func() {
		for i := 0; i < burst; i++ {
			fn(download.ProgressEvent{Current: int64(i) * progressMiB, Total: burst * progressMiB, SpeedBps: progressMiB})
		}
		close(blocked)
	}()
	select {
	case <-blocked:
	case <-time.After(eventWaitTimeout):
		t.Fatal("高频事件把发布路径阻塞了：发布不能等客户端")
	}
	if n := len(client.ch); n != 1 {
		t.Errorf("同一任务 %d 帧高频事件后客户端队列 = %d, want 1（1 小时内只允许一帧）", burst, n)
	}

	// 节流按任务隔离：另一个任务的第一帧照发。
	other := newTestTask("job-other")
	feedOne(t, s, other, 1, 2)
	if n := len(client.ch); n != 2 {
		t.Errorf("其它任务的帧被连坐丢了：队列 = %d, want 2", n)
	}

	// 上限过去（这里改成 0）后同一任务照发：节流是限速，不是一次性闩锁。
	s.progressPublishInterval = 0
	feedOne(t, s, task, 10, 100)
	if n := len(client.ch); n != 3 {
		t.Errorf("上限过去后同一任务仍被静音：队列 = %d, want 3", n)
	}

	// 上限关掉后的高频事件：发布依旧不阻塞，客户端队列封顶在 hub 的深度（多出来的丢帧）。
	flooded := make(chan struct{})
	go func() {
		for i := 0; i < eventClientBuffer*8; i++ {
			fn(download.ProgressEvent{Current: int64(i), Total: eventClientBuffer * 8, SpeedBps: progressMiB})
		}
		close(flooded)
	}()
	select {
	case <-flooded:
	case <-time.After(eventWaitTimeout):
		t.Fatal("高频事件把发布路径阻塞了（hub 必须做非阻塞发送）")
	}
	if n := len(client.ch); n != eventClientBuffer {
		t.Errorf("无节流时客户端队列 = %d, want %d（超出即丢帧，队列不能被打爆）", n, eventClientBuffer)
	}
}
