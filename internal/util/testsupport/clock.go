package testsupport

import "time"

// nowPlusHour 是给状态文件造「未来 mtime」的位移量：+1 小时远大于任何文件系统的 mtime 粒度
// （NTFS 100ns / APFS 1ns / HFS+ 1s），所以「只动 mtime」这个场景在三平台上都成立。
func nowPlusHour() time.Time { return time.Now().Add(time.Hour) }
