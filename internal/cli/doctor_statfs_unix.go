//go:build !windows

package cli

import "syscall"

// statfsFree 返回目录所在文件系统的剩余字节（Unix）。
func statfsFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
