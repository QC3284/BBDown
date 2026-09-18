package workflow

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/util"
)

// 本文件把上游 BBDown.Tests/UrlResolverTests.cs 的输入表搬过来当差分测试。
// 只跑不依赖网络的输入（短链/ss/md 需要请求接口，另有用例覆盖）。
func TestUpstreamResolveURLBareInputs(t *testing.T) {
	ctx := context.Background()
	client := util.NewHTTPClient(nil, func() string { return "" }, nil)

	// 期望得到确定的 aid 字符串
	same := []struct{ in, want string }{
		{"https://www.bilibili.com/video/av170001", "170001"},
		{"https://www.bilibili.com/video/Av170001", "170001"},
		{"av170001", "170001"},
		{"AV170001", "170001"},
		{"av12345", "12345"},
		{"AV99999", "99999"},
		{"ep:12345", "ep:12345"},
		{"ep123", "ep:123"},
		{"cheese:123", "cheese:123"},
		{"mid:12345", "mid:12345"},
		{"mid:123", "mid:123"},
		{"favId:1:2", "favId:1:2"},
		{"listBizId:123", "listBizId:123"},
		{"seriesBizId:123", "seriesBizId:123"},
		{"ep:123", "ep:123"},
	}
	for _, c := range same {
		got, err := ResolveURL(ctx, client, c.in)
		if err != nil {
			t.Errorf("ResolveURL(%q) 报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveURL(%q) = %q, 上游期望 %q", c.in, got, c.want)
		}
	}

	// BV：上游 BilibiliBvConverterTests 给的是确定的 aid，这里按同样三个值断言
	// （前缀大小写都要容忍）。
	for in, want := range map[string]string{
		"BV17x411w7KC": "170001",
		"bv17x411w7KC": "170001",
		"BV1Q541167Qg": "455017605",
		"BV1mK4y1C7Bz": "882584971",
	} {
		got, err := ResolveURL(ctx, client, in)
		if err != nil {
			t.Errorf("ResolveURL(%q) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ResolveURL(%q) = %q，上游期望 %q", in, got, want)
		}
	}

	for _, in := range []string{
		"https://www.bilibili.com/video/BV1xx411c7mD",
		"https://www.bilibili.com/video/bv1xx411c7mD",
		"BV1xx411c7mD",
		"bv1xx411c7mD",
	} {
		got, err := ResolveURL(ctx, client, in)
		if err != nil {
			t.Errorf("ResolveURL(%q) 报错: %v", in, err)
			continue
		}
		aid, perr := strconv.ParseInt(got, 10, 64)
		if perr != nil || aid <= 0 {
			t.Errorf("ResolveURL(%q) = %q，上游期望解出正数 aid", in, got)
		}
	}
}

// 上游 UrlResolverTests 对非法输入要求报错，且 "bv" 这类过短输入要给出「无法识别」。
func TestUpstreamResolveURLRejects(t *testing.T) {
	ctx := context.Background()
	client := util.NewHTTPClient(nil, func() string { return "" }, nil)

	if got, err := ResolveURL(ctx, client, "invalid_input"); err == nil {
		t.Errorf("ResolveURL(\"invalid_input\") = %q，上游要求报错", got)
	}
	_, err := ResolveURL(ctx, client, "bv")
	if err == nil {
		t.Fatal("ResolveURL(\"bv\") 应报错")
	}
	if !strings.Contains(err.Error(), "无法识别") {
		t.Errorf("错误信息 %q，上游含「无法识别」", err)
	}
}
