package util

import (
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

// parseQRArt 把 renderQRCode 的输出解析回模块矩阵：true 表示深色模块。
func parseQRArt(t *testing.T, art string) [][]bool {
	t.Helper()
	rows := strings.Split(strings.TrimRight(art, "\n"), "\n")
	out := make([][]bool, 0, len(rows))
	for _, row := range rows {
		var line []bool
		// 一行的形状是 <色><块><色><块>…<复位>，块是两格宽的实心方块。
		parts := strings.Split(strings.TrimSuffix(row, AnsiReset), blockGlyph)
		if parts[len(parts)-1] != "" {
			t.Fatalf("行 %q 末尾不是模块", row)
		}
		for _, color := range parts[:len(parts)-1] {
			switch {
			case strings.HasPrefix(color, AnsiBlack):
				line = append(line, true)
			case strings.HasPrefix(color, AnsiWhite):
				line = append(line, false)
			default:
				t.Fatalf("无法识别的模块色 %q", color)
			}
		}
		out = append(out, line)
	}
	return out
}

// TestQRCodePolarityMatchesUpstream 钉住二维码的明暗极性：上游
// ConsoleQRCode.GetGraphic 把 ModuleMatrix 为 true 的模块画成 ConsoleColor.Black。
// QR 规范规定定位图案的外框恒为深色、septum 分隔符恒为浅色，所以这条断言不依赖
// 本实现自己的编码逻辑——极性画反时整张码变反色，用例必红。
func TestQRCodePolarityMatchesUpstream(t *testing.T) {
	qr, err := qrcode.New("https://example.com/bbdown-qr-polarity", qrcode.Medium)
	if err != nil {
		t.Fatalf("qrcode.New: %v", err)
	}
	got := parseQRArt(t, renderQRCode(qr))
	bitmap := qr.Bitmap()

	if len(got) != len(bitmap) {
		t.Fatalf("渲染 %d 行，期望 %d 行", len(got), len(bitmap))
	}
	for y := range bitmap {
		if len(got[y]) != len(bitmap[y]) {
			t.Fatalf("第 %d 行渲染 %d 个模块，期望 %d 个", y, len(got[y]), len(bitmap[y]))
		}
	}

	// 定位图案（含 4 模块静默区）原点：行优先扫到的第一个深色模块。
	y0, x0 := -1, -1
scan:
	for y := range bitmap {
		for x := range bitmap[y] {
			if bitmap[y][x] {
				y0, x0 = y, x
				break scan
			}
		}
	}
	if y0 < 0 {
		t.Fatal("未在 bitmap 中找到定位图案")
	}
	if !got[y0][x0] {
		t.Errorf("定位图案外框左上角应为深色(ConsoleColor.Black)，实际画成浅色：二维码整体反色")
	}
	if got[y0+1][x0+1] {
		t.Errorf("定位图案 (1,1) 应为浅色，实际画成深色：二维码整体反色")
	}
	if !got[y0+2][x0+2] {
		t.Errorf("定位图案中心 3x3 应为深色，实际画成浅色")
	}
	if got[y0+7][x0+7] {
		t.Errorf("定位图案分隔符(septum)应为浅色，实际画成深色")
	}

	// 逐模块比对：任何一处错位或极性翻转都要暴露。
	for y := range bitmap {
		for x := range bitmap[y] {
			if got[y][x] != bitmap[y][x] {
				t.Fatalf("模块 (%d,%d) 明暗不符：bitmap=%v，终端=%v", y, x, bitmap[y][x], got[y][x])
			}
		}
	}
}
