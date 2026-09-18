package util

import (
	"os"
	"path/filepath"
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

// ExecutableDir returns the directory containing the running executable
// (shared by credential/config/subscription file resolution).
func ExecutableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// maxFileNameRunes bounds a single path component produced from a template.
const maxFileNameRunes = 100

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

	// Windows rejects a trailing dot or space ("video." / "video ") and silently
	// drops it, so the file lands under an unexpected name. The basename is capped
	// too: template-built names such as "<videoTitle>_<dfn>_<fps>" can exceed the
	// per-component budget, and cutting one mid-rune would produce invalid UTF-8.
	trimmed := strings.TrimRight(result, " .")
	if trimmed == "" && result != "" {
		// Nothing but dots and spaces: Windows rejects that outright, so fall back to
		// a usable component. An empty input stays empty — callers use it to mean
		// "no value".
		trimmed = replacement
	}
	result = truncateRunes(trimmed, maxFileNameRunes)

	// Handle reserved names: Windows treats CON/PRN/AUX/NUL/COM1..9/LPT1..9 as
	// reserved even WITH an extension (CON.txt), so match the basename too.
	base := result
	if idx := strings.LastIndexByte(base, '.'); idx >= 0 {
		base = base[:idx]
	}
	if reservedNames[strings.ToUpper(result)] || reservedNames[strings.ToUpper(base)] {
		result = "_" + result
	}

	return result
}

// SanitizePathSegment neutralises a server-controlled value before it is
// interpolated into a file-name placeholder (<aid>, <cid>, <dfn>, <res>,
// <fps>, <videoCodecs>, <audioCodecs>, subtitle lan ...). Those values come
// from API responses, so a mirror or a --insecure MITM can inject path
// separators or ".." and write outside the target directory (upstream
// PathUtil.SanitizePathSegment, RF-48/58/63/73). Legitimate values are
// returned byte-for-byte unchanged.
func SanitizePathSegment(s string) string {
	if s == "" {
		return s
	}
	dirty := false
	for _, r := range s {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			dirty = true
			break
		}
	}
	if !dirty && s != "." && s != ".." {
		return s
	}
	out := GetValidFileName(s, "_", true)
	out = strings.Trim(out, " .")
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}
