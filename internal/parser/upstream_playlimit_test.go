package parser

import (
	"encoding/json"
	"strings"
	"testing"
)

// 本文件搬上游 ParserPlayLimitTests：播放限制要给出**原因**，业务错误码只在
// 根是对象且 code 是 JSON 数字时才算错误。

func obj(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestUpstreamThrowIfPlayLimited(t *testing.T) {
	err := throwIfPlayLimited(obj(t, `{"result":{"play_check":{"limit_play_reason":"AREA_LIMIT","play_detail":"PLAY_NONE"}}}`))
	if err == nil {
		t.Fatal("区域限制必须报错")
	}
	for _, want := range []string{"区域限制", "limit_play_reason=AREA_LIMIT", "play_detail=PLAY_NONE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺 %q: %v", want, err)
		}
	}

	// 其余原因也要有各自的说明
	cases := map[string]string{
		"PAY_LIMIT": "付费限制",
		"VIP_LIMIT": "大会员",
		"TIME_LOCK": "尚未到可播放时间",
		"OTHER_XYZ": "播放限制",
	}
	for reason, want := range cases {
		err := throwIfPlayLimited(obj(t, `{"result":{"play_check":{"limit_play_reason":"`+reason+`"}}}`))
		if err == nil {
			t.Errorf("%s 应报错", reason)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s 的说明应含 %q，实际 %v", reason, want, err)
		}
	}

	// 无 play_check / 两者都为空 → 不报错
	if err := throwIfPlayLimited(obj(t, `{"result":{}}`)); err != nil {
		t.Errorf("没有 play_check 不应报错: %v", err)
	}
	if err := throwIfPlayLimited(obj(t, `{"result":{"play_check":{}}}`)); err != nil {
		t.Errorf("reason/detail 都为空不应报错: %v", err)
	}
}

func TestUpstreamThrowIfBizError(t *testing.T) {
	// 非 0 数字 code：报错并带上 message
	err := throwIfBizError(obj(t, `{"code":86038,"message":"无法观看"}`))
	if err == nil {
		t.Fatal("非 0 code 必须报错")
	}
	for _, want := range []string{"86038", "无法观看"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺 %q: %v", want, err)
		}
	}

	// 0 / 缺失 / 字符串 code / 根非对象 → 不报错
	for _, js := range []string{
		`{"code":0,"message":"success","data":{}}`,
		`{"data":{}}`,
		`{"code":"-412","data":{}}`,
	} {
		if err := throwIfBizError(obj(t, js)); err != nil {
			t.Errorf("%s 不应报错: %v", js, err)
		}
	}
	if err := throwIfBizError(map[string]interface{}{}); err != nil {
		t.Errorf("空对象不应报错: %v", err)
	}
	// message 缺失时给出兜底文案
	err = throwIfBizError(obj(t, `{"code":-404}`))
	if err == nil || !strings.Contains(err.Error(), "-404") {
		t.Errorf("message 缺失时应给出含 code 的兜底文案: %v", err)
	}
}

func TestUpstreamIsVipRestrictedResponse(t *testing.T) {
	jsonCases := []struct {
		json string
		want bool
	}{
		{`{"code":-10403,"message":"大会员专享限制"}`, true},
		{`{"code":-10403,"message":"大会员专享限制","data":{}}`, true},
		{`{"code":0,"message":"success","data":{}}`, false}, // 正常响应
		{`{"code":-10403,"message":"版权受限"}`, false},         // 其它限制文案不算大会员
		{`{"data":{}}`, false}, // 无 message
	}
	for _, c := range jsonCases {
		if got := isVipRestricted(c.json); got != c.want {
			t.Errorf("isVipRestricted(%s) = %v，上游期望 %v", c.json, got, c.want)
		}
	}

	// 非 JSON：回落到裸子串匹配
	if !isVipRestricted(`<html>window.__playinfo__="大会员专享限制"</html>`) {
		t.Error("非 JSON 页面应回落到子串匹配")
	}
	if isVipRestricted("<html>risk-control</html>") {
		t.Error("非 JSON 且无命中时不应判为大会员限制")
	}
}
