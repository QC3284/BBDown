package download

import "sync"

// progressAggregator 是上游 BBDownDownloadUtil.ProgressAggregator 的移植（RF-75）：
// 分片回调上报的是「该分片自己的累计字节数」而不是增量，所以聚合要用「新值 - 上次值」
// 推进总量；分片重试时上报值回退（重新从 0 开始），总量也必须随之回退，
// 否则进度条会越过 100%。
//
// 只按「分片完成」累加是不够的：默认分片数下进度条会以 25% 为台阶跳变，
// 而用户看到的两次「进度」正是这种跳变与收尾竞态叠加的结果。
type progressAggregator struct {
	mu      sync.Mutex
	perClip []int64
	total   int64
}

func newProgressAggregator(clipCount int) *progressAggregator {
	return &progressAggregator{perClip: make([]int64, clipCount)}
}

// Report 上报某分片当前的累计下载字节数，返回跨分片总量。
func (a *progressAggregator) Report(index int, cumulativeForClip int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.total += cumulativeForClip - a.perClip[index]
	a.perClip[index] = cumulativeForClip
	return a.total
}

// Total 返回当前跨分片累计总量。
func (a *progressAggregator) Total() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.total
}
