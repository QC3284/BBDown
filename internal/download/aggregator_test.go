package download

import (
	"sync"
	"testing"
)

// TestProgressAggregatorMatchesUpstream 移植上游 BBDown.Tests/DownloadProgressAggregationTests.cs：
// 分片回调给的是「该分片自己的累计字节数」，聚合用「新值 - 上次值」推进；
// 分片重试时上报值回退，总量必须跟着回退，否则进度条会越过 100%。
func TestProgressAggregatorMatchesUpstream(t *testing.T) {
	t.Run("交错推进等于各分片之和", func(t *testing.T) {
		const clips, steps = 8, 10
		const block = int64(262144)
		agg := newProgressAggregator(clips)
		reference := make([]int64, clips)
		for step := 0; step < steps; step++ {
			for c := 0; c < clips; c++ {
				cumulative := int64(step+1) * block
				reference[c] = cumulative
				var want int64
				for _, v := range reference {
					want += v
				}
				if got := agg.Report(c, cumulative); got != want {
					t.Fatalf("Report(%d, %d) = %d, 期望 %d", c, cumulative, got, want)
				}
			}
		}
		if got, want := agg.Total(), int64(clips)*steps*block; got != want {
			t.Fatalf("Total() = %d, 期望 %d", got, want)
		}
	})

	t.Run("并发上报总量精确", func(t *testing.T) {
		const clips, steps = 64, 500
		const block = int64(262144)
		agg := newProgressAggregator(clips)
		var wg sync.WaitGroup
		for c := 0; c < clips; c++ {
			wg.Add(1)
			go func(c int) {
				defer wg.Done()
				for step := 0; step < steps; step++ {
					agg.Report(c, int64(step+1)*block)
				}
			}(c)
		}
		wg.Wait()
		if got, want := agg.Total(), int64(clips)*steps*block; got != want {
			t.Fatalf("Total() = %d, 期望 %d", got, want)
		}
	})

	t.Run("重试后总量回退", func(t *testing.T) {
		const block = int64(262144)
		agg := newProgressAggregator(3)
		agg.Report(0, 10*block)
		if got := agg.Report(1, 10*block); got != 20*block {
			t.Fatalf("Report(1, 10*block) = %d, 期望 %d", got, 20*block)
		}
		// 分片 1 下载失败后从头重下：上报回到 0，总量必须跟着回退
		if got := agg.Report(1, 0); got != 10*block {
			t.Fatalf("重试回退后 = %d, 期望 %d", got, 10*block)
		}
		if got := agg.Report(1, 3*block); got != 13*block {
			t.Fatalf("Report(1, 3*block) = %d, 期望 %d", got, 13*block)
		}
	})
}
