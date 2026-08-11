package util

import (
	"strings"
)

// Invalid characters for file names, matching Windows restrictions.
var invalidFileNameChars = []byte{
	'"', '<', '>', '|', 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
	':', '*', '?', '\\', '/',
}

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// GetValidFileName replaces invalid filename characters and handles reserved names.
func GetValidFileName(input string, replacement string, filterSlash bool) string {
	if replacement == "" {
		replacement = "_"
	}

	// Build a set of chars to replace
	invalidSet := make(map[rune]bool, len(invalidFileNameChars))
	for _, c := range invalidFileNameChars {
		invalidSet[rune(c)] = true
	}

	var sb strings.Builder
	for _, r := range input {
		if invalidSet[r] {
			sb.WriteString(replacement)
		} else if filterSlash && (r == '/' || r == '\\') {
			sb.WriteString(replacement)
		} else {
			sb.WriteRune(r)
		}
	}

	result := sb.String()

	// Handle reserved names
	if reservedNames[strings.ToUpper(result)] {
		result = "_" + result
	}

	return result
}
