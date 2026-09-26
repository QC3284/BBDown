package cli

import "testing"

// 机读模式判定：横幅/链接/日志都要让位给数据。
//
// 变异验证：把判定函数改成恒 false → 本用例变红。
func TestIsMachineReadableArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--info-json", "BV1"},
		{"BV1", "--info-json"},
		{"doctor", "--json"},
		{"--json=true", "doctor"},
	} {
		if !IsMachineReadableArgs(args) {
			t.Errorf("%v 应当判定为机读模式", args)
		}
	}
	for _, args := range [][]string{
		{"BV1"},
		{"-I", "BV1"},
		{"--print-urls", "BV1"},
		{"doctor"},
	} {
		if IsMachineReadableArgs(args) {
			t.Errorf("%v 不该判定为机读模式（人看的输出要保持原有清爽样式）", args)
		}
	}
}
