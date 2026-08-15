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

	// Convert QR code to ASCII art
	bitmap := qr.Bitmap()
	var sb strings.Builder
	for _, row := range bitmap {
		for _, col := range row {
			if col {
				sb.WriteString("\033[47m  \033[0m") // white block
			} else {
				sb.WriteString("\033[40m  \033[0m") // black block
			}
		}
		sb.WriteByte('\n')
	}
	fmt.Println(sb.String())
	return nil
}
