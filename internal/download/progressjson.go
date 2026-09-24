package download

import (
	"encoding/json"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// F5 进度事件 JSON（--progress-json，本仓新功能；上游只有终端进度条）。
//
// 关闭时（默认）一切照旧：终端进度条原地重绘。打开时把进度改成**逐行 JSON**：
// 一行一个事件、写到 stderr——stdout 要留给产物路径与正常日志，
// 外部集成（GUI / 自动化）才能把 stdout 当数据流用而不被进度帧污染。
var progressJSONEnabled atomic.Bool

// SetProgressJSON 由 CLI 的 --progress-json 打开/关闭 JSON 进度事件（默认关）。
func SetProgressJSON(enabled bool) { progressJSONEnabled.Store(enabled) }

// progressJSONOut 返回事件输出目标，默认 stderr。
// 变量而非常量：用例据此断言「事件写 stderr，不污染 stdout」。
var progressJSONOut = func() io.Writer { return os.Stderr }

// progressEvent 是一条进度事件，json 字段名即对外契约。
type progressEvent struct {
	Percent    float64 `json:"percent"`    // 0~100
	Downloaded int64   `json:"downloaded"` // 已下载字节
	Total      int64   `json:"total"`      // 总字节（未知为 0）
	Speed      float64 `json:"speed"`      // 字节/秒（1 秒滑窗，未结算时为 0）
	State      string  `json:"state"`      // progress=进行中, done=本次传输结束
}

// jsonProgressEmitter 把「已下载字节」渲染成逐行 JSON。只在渲染协程里使用
// （与终端进度条同一约束：速度状态属于协程私有，无需加锁）。
type jsonProgressEmitter struct {
	total int64
	last  time.Time
	lastB int64
	speed float64
}

func newJSONProgressEmitter(total int64) *jsonProgressEmitter {
	return &jsonProgressEmitter{total: total, last: time.Now()}
}

// progress 发一条进行中的事件。
func (e *jsonProgressEmitter) progress(downloaded int64) { e.write(downloaded, "progress") }

// done 发一条结束事件：调用方在下载/中断收尾时各调一次，消费方据此知道
// 「这个文件的事件流到此为止」，不必靠猜。
func (e *jsonProgressEmitter) done(downloaded int64) { e.write(downloaded, "done") }

func (e *jsonProgressEmitter) write(downloaded int64, state string) {
	now := time.Now()
	// 速度与终端进度条同一口径：满 1 秒结算一次，避免逐帧（最高 60fps）被瞬时抖动带偏。
	// 分片重试回退时 delta 为负，按 0 处理（不能报负速度）。
	if elapsed := now.Sub(e.last).Seconds(); elapsed >= 1.0 {
		delta := downloaded - e.lastB
		if delta < 0 {
			delta = 0
		}
		e.speed = float64(delta) / elapsed
		e.lastB = downloaded
		e.last = now
	}
	pct := float64(0)
	if e.total > 0 {
		pct = float64(downloaded) / float64(e.total) * 100
		if pct > 100 {
			pct = 100
		}
	}
	line, err := json.Marshal(progressEvent{
		Percent:    pct,
		Downloaded: downloaded,
		Total:      e.total,
		Speed:      e.speed,
		State:      state,
	})
	if err != nil {
		return
	}
	w := progressJSONOut()
	if w == nil {
		return
	}
	// 一次 Write 写完「JSON + 换行」：消费方按行切分即可，不会读到半行。
	_, _ = w.Write(append(line, '\n'))
}
