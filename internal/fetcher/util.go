package fetcher

import (
	"fmt"

	"github.com/QC3284/BBDown/internal/entity"
)

// ---- map[string]interface{} JSON helpers for fetchers ----

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

func gi(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case string:
		n := 0
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func gi64(m map[string]interface{}, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case string:
		var n int64
		fmt.Sscanf(v, "%d", &n)
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
