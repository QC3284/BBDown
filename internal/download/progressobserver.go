package download

import "context"

// 下载层的「逐字节进度观察者」。
//
// 此前进度只写未导出的 progressJSONOut（stderr），进程内的上层（serve 的 SSE）拿不到
// 真实字节数，只能报「入队 / 开始 / 拿到元数据 / 产物落盘 / 终止」这类**服务端可观测边界**
// 的事件。观察者把渲染路径上的一帧转成一次回调，上层据此推真实进度。
//
// 用 context 携带，**不用包级变量**：本仓刚因「全局可变状态」栽过一次（台账 §4.56）——
// 全局观察者会让并发的两次下载互相串台，也会让「没装观察者」不再等于「不进回调路径」。
// 观察者跟着 ctx 走，天然按任务隔离，也不必往 DownloadConfig 里加字段（下载配置的
// 传递面因此不变）。
type progressObserverKey struct{}

// ProgressEvent 是一帧进度。
//
// Current/Total 都**含续传 base**：它们与 --progress-json 的 downloaded/total 逐字同义
// （整份文件口径，见 progressReader.withBase/wholeTotal），不是「本次响应读了多少」。
// Total==0 表示长度未知（拿不到 Content-Length），此时不要按百分比展示。
// SpeedBps 是最近一个 ≥1 秒窗口结算出的速率（字节/秒），0 表示还没结算出来。
type ProgressEvent struct {
	Current  int64
	Total    int64
	SpeedBps float64
}

// WithProgressObserver 把 fn 挂到 ctx 上，返回派生的 ctx。fn 为 nil 时原样返回 ctx
// （等于没挂，ProgressObserverFromContext 仍返回 nil）。
//
// 回调在**进度渲染协程**里被同步调用，并且只在既有节流之后（与终端进度帧 /
// --progress-json 事件同一频率，见 runProgressLoop），所以高频下载不会把回调/SSE 打爆。
//
// **回调必须快速返回**：渲染协程同时负责等下载收尾（progressReader.Close 要等它），
// 阻塞在这里会拖慢进度收尾；实现侧调用时不持任何锁（要用的数字先复制出来再调），
// 因此回调里即使加锁也不会反过来卡住下载的字节计数。
func WithProgressObserver(ctx context.Context, fn func(ProgressEvent)) context.Context {
	if ctx == nil || fn == nil {
		return ctx
	}
	return context.WithValue(ctx, progressObserverKey{}, fn)
}

// ProgressObserverFromContext 取出挂在 ctx 上的观察者；没有则返回 nil
// （调用方据此完全不进回调路径——未装观察者时下载层行为与改前一致）。
func ProgressObserverFromContext(ctx context.Context) func(ProgressEvent) {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(progressObserverKey{}).(func(ProgressEvent))
	return fn
}
