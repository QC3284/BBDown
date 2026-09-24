package fetcher

import (
	"strings"
	"testing"
)

// TestThrowIfAPIErrorMatchesUpstream 对应上游 v1.6.20 新增的 FetcherJson.ThrowIfApiError。
//
// 上游此前有六处写成「if (code != 0) { var msg = SanitizeServerText(...); }」——只算不抛，
// 精心写好的诊断永远不可达；v1.6.20 收口成这一处。本仓这些路径一直是直接 return err，但
// 「data 存在时也要查 code」此前没有覆盖：带错误 code 却带 data 节点的响应会被当作有效
// 合集/系列解析（错误响应通常 data=null，所以这是窄边界，但边界就是边界）。
//
// 变异验证：把 code 判断改成恒不触发，本用例变红。
func TestThrowIfAPIErrorMatchesUpstream(t *testing.T) {
	if err := throwIfAPIError(map[string]interface{}{"code": 0.0, "data": map[string]interface{}{}}, "获取系列信息失败"); err != nil {
		t.Errorf("code=0 不该报错：%v", err)
	}
	if err := throwIfAPIError(map[string]interface{}{}, "获取系列信息失败"); err != nil {
		t.Errorf("缺 code 按 0 处理，不该报错：%v", err)
	}

	err := throwIfAPIError(map[string]interface{}{
		"code": -404.0, "message": "啥都木有", "data": map[string]interface{}{"title": "x"},
	}, "获取系列信息失败")
	if err == nil {
		t.Fatal("code 非零必须报错，哪怕 data 存在")
	}
	if want := "获取系列信息失败 (code=-404): 啥都木有"; err.Error() != want {
		t.Errorf("消息格式须与上游 FetcherJson 逐字一致：got %q, want %q", err.Error(), want)
	}

	// 服务器把 code 写成字符串（上游 GetInt64Safe 也认）：同样要拦下。
	err = throwIfAPIError(map[string]interface{}{"code": "-412", "message": "请求被拦截"}, "获取合集信息失败")
	if err == nil || !strings.Contains(err.Error(), "code=-412") {
		t.Errorf("字符串形态的 code 也要拦：%v", err)
	}

	// 缺 message：不 panic，且 code 不能丢。
	err = throwIfAPIError(map[string]interface{}{"code": -412.0}, "获取合集视频列表失败")
	if err == nil || !strings.Contains(err.Error(), "获取合集视频列表失败 (code=-412)") {
		t.Errorf("缺 message 时仍须带文案与 code：%v", err)
	}
}
