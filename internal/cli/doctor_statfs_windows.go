//go:build windows

package cli

import "fmt"

// statfsFree: Windows 上不实现（doctor 会降级成只报「目录可写」）。
func statfsFree(dir string) (uint64, error) {
	return 0, fmt.Errorf("statfs unsupported on windows")
}
