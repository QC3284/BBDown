package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 版本号硬编码在五处（横幅、root.Version、两处更新检查、PKGBUILD），靠人记着改必然漂移。
// 这条用例把它们扫出来比对，并检查格式符合版本规则（MAJOR.MINOR.PATCH[-rcN]，见 AGENTS.md）。
//
// 变异验证：把任意一处改成别的版本 → 变红。
func TestVersionStringsAreConsistent(t *testing.T) {
	root := filepath.Join("..", "..")
	// root.go 的 `Version:` 是事实来源（cobra 命令字段，不是包级变量），其余四处必须与它一致。
	files := []struct {
		path    string
		pattern string
	}{
		{filepath.Join(root, "internal", "cli", "root.go"), `Version: "([0-9][^"]*)"`},
		{filepath.Join(root, "cmd", "bbdown", "main.go"), `BBDown version ([0-9][^,]*),`},
		{filepath.Join(root, "internal", "cli", "root.go"), `"v([0-9][^"]*)"`},
		{filepath.Join(root, "internal", "cli", "commands.go"), `"v([0-9][^"]*)"`},
		{filepath.Join(root, "PKGBUILD"), `pkgver=([0-9][^\s]*)`},
	}

	readVersion := func(f struct {
		path    string
		pattern string
	}) (string, bool) {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("读取 %s: %v", f.path, err)
		}
		m := regexp.MustCompile(f.pattern).FindStringSubmatch(string(data))
		if m == nil {
			t.Errorf("%s 里找不到版本号（匹配 %s）——版本号改位置了？", f.path, f.pattern)
			return "", false
		}
		return strings.TrimSpace(m[1]), true
	}

	want, ok := readVersion(files[0])
	if !ok {
		t.FailNow()
	}
	format := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$`)
	if !format.MatchString(want) {
		t.Errorf("版本号 %q 不符合规则 MAJOR.MINOR.PATCH[-rcN]（见 AGENTS.md 版本语义）", want)
	}

	for _, f := range files[1:] {
		got, ok := readVersion(f)
		if ok && got != want {
			t.Errorf("%s 里的版本 = %q，与 root.go 的 %q 不一致（五处必须同步）", f.path, got, want)
		}
	}
}
