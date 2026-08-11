package util

import (
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// PrintQRCode generates and prints a QR code to the terminal.
func PrintQRCode(content string) error {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("generate QR code: %w", err)
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
