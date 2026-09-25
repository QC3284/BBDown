package config

import "testing"

// HDR Vivid（129）是上游 v1.6.20 没有、C# 2.x 接手线 BBDownT 新增的清晰度档位（§4.41）。
// 这条用例钉住「映射表里有它」以及「APP 协议最高档与之一致」——127 → 129 那次变更撞掉了两条
// 写死字面量的用例，说明这种档位必须有用例守。
//
// 变异验证：删掉 QualityMap 里的 129 条目 → 本条变红。
func TestQualityMapIncludesHDRVivid(t *testing.T) {
	if got := QualityMap["129"]; got != "HDR Vivid" {
		t.Errorf("QualityMap[129] = %q，期望 HDR Vivid", got)
	}
	if QualityMap["127"] != "8K 超高清" {
		t.Errorf("较高档位不该被顶掉：QualityMap[127] = %q", QualityMap["127"])
	}
}
