package util

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LooksLikeHTMLPage 判定响应体是否是 HTML 页面（而非预期的 JSON）。
//
// B 站的风控页/登录墙/错误页常以 HTTP 200 + HTML 返回：直接交给 JSON 解析只会得到
// "invalid character '<' looking for beginning of value" 这种难以定位的报错
// （上游为此引入 RiskControlResponseException）。前导空白与 UTF-8 BOM 都要剥掉——
// 部分 WAF/风控页在 '<html' 前带换行或空白，只看首字节会漏检。
func LooksLikeHTMLPage(body string) bool {
	trimmed := strings.TrimLeft(body, " \t\r\n\uFEFF")
	return strings.HasPrefix(trimmed, "<")
}

// RiskControlMessage 是「接口返回 HTML」时给出的可读诊断
// （上游 RiskControlResponseException 的等价文案）。
const RiskControlMessage = "疑似风控页：接口返回 HTML 而非预期数据。可能被 B 站风控拦截，请稍后重试或检查账号/网络状态"

// UnmarshalJSON 是 encoding/json.Unmarshal 的 API 响应版本：
// 响应体其实是 HTML 时先给出可读诊断，而不是裸的 JSON 语法错误。
func UnmarshalJSON(data string, v interface{}) error {
	if LooksLikeHTMLPage(data) {
		return fmt.Errorf("%s", RiskControlMessage)
	}
	return json.Unmarshal([]byte(data), v)
}
