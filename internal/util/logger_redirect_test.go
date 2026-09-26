package util

import (
	"bytes"
	"strings"
	"testing"
)

// RedirectConsoleLogs 是机读模式（--info-json / doctor --json）的底座：日志在作用域内让位到
// 指定流，退出即还原。两条边界必须钉住：
//   - 还原后落点回到**动态解析的 os.Stdout**，而不是冻结在变量里的旧目标——测试惯例
//     「替换 os.Stdout 捕获输出」靠它继续有效；
//   - 作用域结束后不许残留：全局可变目标泄漏会让同包后续用例捕获到空串（f828cba 的坑）。
//
// 变异验证：让位改成空操作 → 第一条断言变红；还原改成空操作 → 第二条变红。
func TestRedirectConsoleLogsScopesAndRestores(t *testing.T) {
	var buf bytes.Buffer
	restore := RedirectConsoleLogs(&buf)
	Log("让位日志 %d", 7)
	LogWarn("让位警告")
	if !strings.Contains(buf.String(), "让位日志 7") || !strings.Contains(buf.String(), "让位警告") {
		t.Errorf("让位期间日志应写进指定流，实际 %q", buf.String())
	}

	// 还原之后：替换 os.Stdout 必须重新能捕获到日志（落点是每次写入时动态解析的）。
	restore()
	out := captureStdout(t, func() { Log("还原后日志 %d", 8) })
	if !strings.Contains(out, "还原后日志 8") {
		t.Errorf("还原后日志应回到 os.Stdout，实际捕获 %q", out)
	}

	// 嵌套：内层退出还原到外层目标，外层退出恢复默认落点。
	outer := &bytes.Buffer{}
	restoreOuter := RedirectConsoleLogs(outer)
	inner := &bytes.Buffer{}
	restoreInner := RedirectConsoleLogs(inner)
	Log("内层")
	restoreInner()
	Log("外层")
	restoreOuter()
	if !strings.Contains(inner.String(), "内层") || strings.Contains(outer.String(), "内层") {
		t.Errorf("内层应只写内层目标：inner=%q outer=%q", inner.String(), outer.String())
	}
	if !strings.Contains(outer.String(), "外层") {
		t.Errorf("内层还原后应回到外层目标，实际 outer=%q", outer.String())
	}
	out = captureStdout(t, func() { Log("全部还原后 %d", 9) })
	if !strings.Contains(out, "全部还原后 9") {
		t.Errorf("外层还原后日志应回到 os.Stdout，实际捕获 %q", out)
	}
}
