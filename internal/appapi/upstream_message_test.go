package appapi

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// 本文件搬上游 AppHelperMessageTests：gRPC 帧头 5 字节，首字节只能 0/1，
// 空载荷合法（返回空数组），gzip 解压要有上限。

func frame(flag byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func TestUpstreamReadMessage(t *testing.T) {
	payload := []byte("hello-grpc")

	// 未压缩往返
	got, err := readMessage(frame(0, payload))
	if err != nil {
		t.Fatalf("未压缩帧解析失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("未压缩往返 = %q，期望 %q", got, payload)
	}

	// 空载荷：合法，返回空数组（不是错误）
	got, err = readMessage(frame(0, nil))
	if err != nil {
		t.Errorf("空载荷应合法（上游返回空数组），实际报错 %v", err)
	} else if len(got) != 0 {
		t.Errorf("空载荷应返回空数组，实际 %q", got)
	}

	// gzip 往返
	got, err = readMessage(packMessage(payload))
	if err != nil {
		t.Fatalf("gzip 帧解析失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("gzip 往返 = %q，期望 %q", got, payload)
	}

	// 非法压缩标志（只能 0/1）
	if _, err := readMessage(frame(2, payload)); err == nil {
		t.Error("非法压缩标志应报错")
	} else if !strings.Contains(err.Error(), "帧") && !strings.Contains(err.Error(), "首字节") {
		t.Errorf("错误信息应指出帧头问题，实际 %q", err)
	}

	// 帧头不足 5 字节
	if _, err := readMessage([]byte{0, 0, 0, 0}); err == nil {
		t.Error("帧头不足 5 字节应报错")
	}

	// gzip 解压超限（小体积压缩炸弹）
	big := make([]byte, 64<<20)
	bomb := packMessage(big)
	if len(bomb) >= len(big)/10 {
		t.Fatalf("gzip 应显著压缩可压缩数据: %d vs %d", len(bomb), len(big))
	}
	if _, err := readMessage(bomb); err == nil {
		t.Error("超过解压上限应报错")
	}
}
