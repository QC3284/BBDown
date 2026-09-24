package fetcher

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
)

// ---- map[string]interface{} JSON helpers for fetchers ----

// throwIfAPIError 对应上游 v1.6.20 新增的 FetcherJson.ThrowIfApiError：顶层 code 非零即终止。
//
// 上游此前有六处写成「if (code != 0) { var msg = SanitizeServerText(...); }」——只算不抛，
// 精心写好的诊断永远不可达；v1.6.20 统一收口成这个助手。本仓这些路径一直是直接 return err，
// 但「data 存在时也要查 code」此前没有覆盖：错误响应通常带 data=null，所以只在窄边界上不同。
//
// 消息格式与上游逐字一致：<文案> (code=N): <message>（括号前有空格）。message 来自服务器，
// 由日志层统一单行化/截断（util.SanitizeLogString）。
func throwIfAPIError(root map[string]interface{}, failureMessage string) error {
	code := gi(root, "code")
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%s (code=%d): %s", failureMessage, code, gs(root, "message"))
}

func gs(m map[string]interface{}, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// gi 对应上游 JsonElementExtensions.GetInt32Safe：数字要能落在 int32 内且为整数，
// 字符串要**整串**是数字（允许两侧空白与正负号），否则取默认值 0。
func gi(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		if v != math.Trunc(v) || v < math.MinInt32 || v > math.MaxInt32 {
			return 0
		}
		return int(v)
	case string:
		// 不能用 fmt.Sscanf("%d")：它接受数字**前缀**，"12abc" 会被当成 12——
		// 上游 int.TryParse 要求整串都是数字，脏数据必须落到默认值。
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// gi64 对应上游 GetInt64Safe（整串解析、超范围取默认值）。
func gi64(m map[string]interface{}, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		if v != math.Trunc(v) || v < math.MinInt64 || v > math.MaxInt64 {
			return 0
		}
		return int64(v)
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

func gm(m map[string]interface{}, key string) map[string]interface{} {
	v, _ := m[key].(map[string]interface{})
	return v
}

func ga(m map[string]interface{}, key string) []interface{} {
	v, _ := m[key].([]interface{})
	return v
}

// pageDup checks whether a page with the same aid+cid already exists.
func pageDup(pages []entity.Page, aid, cid string) bool {
	for _, p := range pages {
		if p.Aid == aid && p.Cid == cid {
			return true
		}
	}
	return false
}
