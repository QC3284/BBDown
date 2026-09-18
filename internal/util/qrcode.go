package util

import (
	"fmt"
	"os"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// PrintQRCode generates and prints a QR code to the terminal, and also
// writes qrcode.png next to the executable (upstream PngByteQRCode behavior,
// pixel size 7), so users without a terminal that renders the ANSI art can
// still scan the file.
func PrintQRCode(content string) error {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("generate QR code: %w", err)
	}

	// Save PNG file (best effort; the terminal art below still works without it).
	if werr := qr.WriteFile(7, "qrcode.png"); werr != nil {
		fmt.Fprintf(os.Stderr, "写入 qrcode.png 失败: %v\n", werr)
	}

	fmt.Println(renderQRCode(qr))
	return nil
}

// blockGlyph is the two-cell solid block each module is drawn with.
const blockGlyph = "██"

// renderQRCode renders the QR code as ANSI art using upstream
// ConsoleQRCode.GetGraphic 的配色：模块为 true 的是**深色**模块，画成
// ConsoleColor.Black；false 的浅色模块画成 ConsoleColor.White（.NET 里 White 是
// 亮白，即 97）。
//
// 曾把极性画反：深色模块用 47（白底）、浅色模块用 40（黑底），整张码变成反色。
// 反色码在深色终端上多数扫码器仍认，但一旦终端是浅色主题就扫不出来，所见也与上游相反。
func renderQRCode(qr *qrcode.QRCode) string {
	var sb strings.Builder
	for _, row := range qr.Bitmap() {
		for _, dark := range row {
			if dark {
				sb.WriteString(AnsiBlack + blockGlyph)
			} else {
				sb.WriteString(AnsiWhite + blockGlyph)
			}
		}
		sb.WriteString(AnsiReset)
		sb.WriteByte('\n')
	}
	return sb.String()
}
