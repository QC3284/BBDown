package workflow

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
)

// 上游 NumericOptionValidationTests 的整张表：非法数值必须在进入下载/混流前报错，
// 否则会变成「没下载任何数据却返回成功」「分片切分不收敛」这类难定位的故障。
func TestUpstreamNumericOptionValidation(t *testing.T) {
	valid := func() config.MyOption {
		cfg := config.DefaultMyOption()
		cfg.URL = "https://www.bilibili.com/video/BV1qt4y1X7TW"
		return cfg
	}
	check := func(t *testing.T, mutate func(*config.MyOption), want string) {
		t.Helper()
		cfg := valid()
		mutate(&cfg)
		w := &Workflow{Cfg: cfg}
		err := w.validateNumericOptions()
		if err == nil {
			t.Fatalf("非法数值未被拒绝")
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息 %q，上游含 %q", err, want)
		}
	}

	// --muxer-timeout：0 / -1 / 溢出值 / MaxInt 都非法
	for _, v := range []int{0, -1, 35792, 1 << 31} {
		check(t, func(c *config.MyOption) { c.MuxerTimeout = v }, "--muxer-timeout")
	}
	// --retry-count：0 与负数非法（0 会导致一次都不下载）
	for _, v := range []int{0, -5, 101} {
		check(t, func(c *config.MyOption) { c.RetryCount = v }, "--retry-count")
	}
	// --thread-segment-size：0 现在是**合法值**（= 自动分片，按并发数倒推，见 testplan/§4.42），
	// 只有负数与超过 1024 才非法。旧契约把 0 判为非法、而 CLI 默认值又改成了 0，
	// 于是默认参数的真实下载一直失败——这条用例当年没发现，因为它的 valid() 写死了 20，
	// 没走「CLI 默认值」这条路（补的守卫见 default_options_test.go）。
	for _, v := range []int{-20, 1025} {
		check(t, func(c *config.MyOption) { c.ThreadSegmentSize = v }, "--thread-segment-size")
	}
	// --retry-delay / --delay-per-page：负数非法
	check(t, func(c *config.MyOption) { c.RetryDelay = -1 }, "--retry-delay")
	check(t, func(c *config.MyOption) { c.DelayPerPage = -1 }, "--delay-per-page")

	// 默认值必须被接受
	if w := (&Workflow{Cfg: valid()}); w.validateNumericOptions() != nil {
		t.Errorf("默认值被拒: %v", w.validateNumericOptions())
	}

	// 边界值（下界与上界）必须被接受
	low := valid()
	low.MuxerTimeout, low.RetryCount, low.RetryDelay, low.ThreadSegmentSize, low.DelayPerPage = 1, 1, 0, 1, 0
	if w := (&Workflow{Cfg: low}); w.validateNumericOptions() != nil {
		t.Errorf("下界被拒: %v", w.validateNumericOptions())
	}
	high := valid()
	high.MuxerTimeout, high.RetryCount, high.RetryDelay, high.ThreadSegmentSize, high.DelayPerPage = 35000, 100, 600000, 1024, 600
	if w := (&Workflow{Cfg: high}); w.validateNumericOptions() != nil {
		t.Errorf("上界被拒: %v", w.validateNumericOptions())
	}
}
