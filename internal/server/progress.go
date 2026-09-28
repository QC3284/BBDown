package server

import (
	"context"
	"math"
	"time"

	"github.com/QC3284/BBDown/internal/download"
)

// 任务 Q：把下载层的「逐字节进度观察者」（download.ProgressEvent，见
// internal/download/progressobserver.go）接到既有 SSE 进度流上。
//
// # 两个进度口径（分工，契约由 progress_contract_test.go 钉住）
//
//   - SSE 帧（GET /events 的 task_progress）：**逐文件真实字节进度**。percent 是 0~100，
//     downloaded/total 是整份文件口径（含续传 base），与 --progress-json 逐字段同义；
//     单一来源是下载层观察者。
//   - 任务 API（/get-tasks）：
//     · TotalDownloadedBytes 是**任务级字节数**：取「按文件身份累计的实时字节」与
//     「已落盘产物字节」的较大者（口径与理由见 DownloadTask.totalBytesLocked）。下载
//     执行期间因此与 SSE 同口径——多产物任务的任务级数字 = 各文件身份**最近一帧**之和
//     （A 报完切到 B 时 A 的值留在映射里，字节数不回跳）；单产物任务就等于最近一帧。
//     · Progress 仍是**边界语义**（0~1）：执行期间 0、成功 1.0（processTask）。它是
//     bbdown-tasks.json 与上游兼容契约消费的字段，**有意**不改成实时比例。
//
// 为什么 Progress 不跟着走：它是任务级比例，而实时分母（各产物的总长）在服务端并不完整
// ——封面/字幕/分离音轨的长度只在各自文件内可见，且多产物任务是串行的。把它做成实时
// 百分比就得引入一个与上游契约不同的语义，收益只是「轮询客户端也能看到百分比」，而
// 需要实时进度的客户端本来就该订阅 /events。
//
// aria2c 路径**没有**观察者（台账 §4.57 记的代价：黑盒，只有开始/结束），产物字节数是
// 那里唯一的进度信号：TotalDownloadedBytes 取两个来源的较大者，正是为了这条路径保持
// 既有边界语义不变（实时映射为空时它就等于产物累计）。
//
// 契约写在 DownloadTask 的字段注释与本段，由 progress_contract_test.go 机械保证
// （SSE 帧是真实字节、任务字段按身份同口径且不回跳、Progress 仍是边界值）。
//
// 本文件只做三件事，且都是**新增**的：既有事件（task_start / 元数据 / 产物落盘 /
// task_done / task_failed）的发布点与字段语义一个都没改（见 events.go 的
// publishTaskEvent）——字节进度是同一任务上多出来的 task_progress 帧，不和它们抢位：
//
//  1. 映射：progressServerEvent（纯函数，表驱动用例逐条钉住口径）；
//  2. 节流：publishDownloadProgress 里按**任务**丢弃过密的帧；
//  3. 接线：把同一帧按**文件身份**记进任务级映射（recordProgressBytesLocked）。
//
// 为什么观察者已经节流了、SSE 侧还要再设一道：观察者的节奏由下载层的进度渲染协程决定
// （minFrameInterval ≈60fps，见 internal/download/pacer.go），那一档是给终端重绘定的；
// 而 SSE 的一帧要经 hub 投进每个在线客户端的队列（64 深）再由各自的处理器写 socket。
// hub 的「队列满丢帧」保证发布者永远不被卡住，但那是**队列满了之后**才生效的兜底；
// 每任务 100ms 的上限让队列在没有慢客户端时也不会被无谓灌满——60fps 的百分比人眼也看不见。

// defaultProgressPublishInterval 是同一任务两帧字节进度之间的最小间隔（≈10 帧/秒/任务）。
// 与 hub 的丢帧语义叠加：即使观察者按 60fps 送帧，队列里也只会有一条任务的最新几帧。
const defaultProgressPublishInterval = 100 * time.Millisecond

// progressServerEvent 把任务身份（base，取自一次加锁快照）与一帧观察者事件映射成既有的
// ServerEvent。
//
// 纯函数：不读时钟、不碰锁、不做 IO——映射表由用例逐条钉住。
// 数字口径与 --progress-json 的 emitter（internal/download/progressjson.go 的 write）逐条对齐：
//
//   - percent = downloaded/total*100，夹到 100；total<=0（拿不到 Content-Length）时
//     percent=0 且 total=0——**不估算**总量，页面按既有的「总量未知」显示；
//   - downloaded/total 是**整份文件**口径：观察者给的 Current/Total 已经含续传 base
//     （progressReader.withBase/wholeTotal），这里不再加/减 base，页面与 --progress-json
//     看到的是同一个数字；
//   - speed 是观察者的 1 秒窗口速率（字节/秒），0 表示还没结算出速率；
//   - downloaded > total 时只夹 percent、downloaded 原样上报：这是「分片计数先加后校验」
//     的瞬时越界（ROADMAP「已完成」里记的既有异常），JSON 侧同样只夹百分比——
//     把字节数也夹住会掩盖真实的计数错误。
//
// 防御：负的 current/total 归 0（JSON 契约里 0 就是「未知」，负值只会来自计数异常），
// NaN/±Inf 的速率归 0——json.Marshal 遇到 NaN/Inf 会**整帧失败**，宁可这一帧报
// 「速率未知」也不能让它凭空消失。
func progressServerEvent(base ServerEvent, ev download.ProgressEvent) ServerEvent {
	downloaded := ev.Current
	if downloaded < 0 {
		downloaded = 0
	}
	total := ev.Total
	if total < 0 {
		total = 0
	}
	speed := ev.SpeedBps
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 {
		speed = 0
	}

	percent := float64(0)
	if total > 0 {
		percent = float64(downloaded) / float64(total) * 100
		if percent > 100 {
			percent = 100
		}
	}

	base.Type = EventTaskProgress
	base.State = StateProgress
	base.Percent = percent
	base.Downloaded = downloaded
	base.Total = total
	base.Speed = speed
	return base
}

// taskDownloadContext 在任务 ctx 上装好逐字节进度观察者，返回给 workflow.Run 使用
// （下载层从 ctx 取观察者：download.WithProgressObserver / ProgressObserverFromContext）。
//
// 抽成方法而不是内联在 processTask 里，是为了让**接线本身**可以被离线用例驱动：
// 用例拿这个 ctx 取出观察者、自己喂一帧，就能断言「事件经 hub 到达 SSE 客户端」，
// 不必真下载（真下载要联网，见 AGENTS 的测试纪律）。去掉这里的 WithProgressObserver
// 接线，用例立刻变红。
//
// 返回值只派生一个值、不带 cancel：ctx 的取消仍由任务自己的 cancelFn 控制。
func (s *APIServer) taskDownloadContext(ctx context.Context, task *DownloadTask) context.Context {
	return download.WithProgressObserver(ctx, func(ev download.ProgressEvent) {
		s.publishDownloadProgress(task, ev)
	})
}

// publishDownloadProgress 是观察者的接线点：按每任务最小间隔把一帧字节进度发成 SSE 事件。
//
// 调用方是下载层的进度渲染协程（契约见 download.WithProgressObserver 的注释）：**必须快速
// 返回**——渲染协程同时负责等下载收尾（progressReader.Close 要等它）。这里只做
// 「一次加锁读快照 + 一次非阻塞广播」：hub.publish 在锁内做非阻塞发送（队列满的客户端丢帧），
// 不做 IO、不等任何客户端，所以慢浏览器不会拖慢下载。
//
// 节流采用「同一任务 ≥progressPublishInterval 才发一帧」，被丢掉的帧**不补发**（不排队）。
// 末帧因此可能被丢，但收尾不会停在半路：成功时既有的 task_done 帧带 percent=100
// （processTask 把 task.Progress 置 1.0，走的是 publishTaskEvent 那条既有路径），
// 页面在任务进入终态后也不再把它当「当前任务」渲染（见 webui/index.html 的 renderCurrent）。
//
// 字节映射**接线在同一段临界区里**：被节流丢掉的帧同样记进任务级映射。丢掉一帧只是
// 少一次 SSE 广播，任务级字节数仍要跟上最新观察值——否则 /get-tasks 会落后于 SSE，
// 正是这次要统一的口径。撤掉 recordProgressBytesLocked 这条接线，
// progress_contract_test.go 的任务级字节断言立刻变红（变异验证）。
func (s *APIServer) publishDownloadProgress(task *DownloadTask, ev download.ProgressEvent) {
	if s.events == nil {
		return
	}
	now := time.Now()
	task.mu.Lock()
	task.recordProgressBytesLocked(ev.Key, ev.Current)
	if !task.lastProgressPublish.IsZero() && now.Sub(task.lastProgressPublish) < s.progressPublishInterval {
		task.mu.Unlock()
		return
	}
	task.lastProgressPublish = now
	// 身份字段随新帧一起带出：页面把「同一 job_id 的最新一条事件」整体替换（tasks.set），
	// 少带的字段会把标题/链接抹成空。
	base := ServerEvent{
		JobID:  task.JobID,
		Aid:    task.Aid,
		URL:    task.URL,
		Title:  task.Title,
		Status: task.Status,
	}
	task.mu.Unlock()
	s.events.publish(progressServerEvent(base, ev))
}
