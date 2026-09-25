package workflow

import (
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
)

// 默认配置必须通过参数校验——这条用例是补出来的漏网之鱼：
//
// O4 把 --thread-segment-size 的默认值改成 0（=自动），但校验器仍只允许 1~1024，于是**默认参数的真实
// 下载从 1.6.20-go.3 起一直直接失败**，而单元测试与 CI 全绿（没有用例走「CLI 默认值 → 校验」这条路）。
//
// 变异验证：把校验改回 `< 1` → 本用例变红。
func TestDefaultOptionsPassValidation(t *testing.T) {
	cfg := config.DefaultMyOption()
	wf := New(cfg, util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil))
	if err := wf.validateNumericOptions(); err != nil {
		t.Fatalf("默认配置必须通过校验，实际报错：%v", err)
	}

	// 自动分片（0）与显式分片都必须合法；越界要拦住。
	for _, size := range []int{0, 1, 20, 1024} {
		cfg.ThreadSegmentSize = size
		wf = New(cfg, util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil))
		if err := wf.validateNumericOptions(); err != nil {
			t.Errorf("--thread-segment-size=%d 应当合法，实际：%v", size, err)
		}
	}
	cfg.ThreadSegmentSize = 2048
	wf = New(cfg, util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil))
	if err := wf.validateNumericOptions(); err == nil {
		t.Error("--thread-segment-size=2048 应当被拦住")
	}
}
