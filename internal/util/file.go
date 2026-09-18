package util

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
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

	// 基名+扩展名截断（上游 PathUtil.GetValidFileName 的同一分支）：只在超长时生效，
	// 扩展名（≤10 字符）保留、只截基名——混流与播放器靠扩展名识别类型，把 ".mp4" 截掉
	// 会产出无法识别的文件；基名未超限时整名不动（总长略超上限但单组件仍在 Windows
	// 255 的预算内）。按 rune 计数，避免把多字节字符切成无效 UTF-8。
	//
	// 顺序与上游一致：截断 → 保留名 → 裁剪尾随点/空格——这样「截断后才出现在末尾的点」
	// 也会被裁掉。
	if utf8.RuneCountInString(result) > maxFileNameRunes {
		result = truncateFileName(result, maxFileNameRunes)
	}

	// Handle reserved names: Windows treats CON/PRN/AUX/NUL/COM1..9/LPT1..9 as
	// reserved even WITH an extension (CON.txt), so match the basename too.
	base := result
	if idx := strings.LastIndexByte(base, '.'); idx >= 0 {
		base = base[:idx]
	}
	if reservedNames[strings.ToUpper(result)] || reservedNames[strings.ToUpper(base)] {
		result = "_" + result
	}

	// Windows rejects a trailing dot or space ("video." / "video ") and silently
	// drops it, so the file lands under an unexpected name.
	trimmed := strings.TrimRight(result, " .")
	if trimmed == "" {
		// Nothing but dots and spaces: Windows rejects that outright, so fall back to
		// a usable component. An empty input stays empty — callers use it to mean
		// "no value"（本仓对空串的既有契约，与上游的 "_" 不同，已记入对齐文档）。
		if result == "" {
			return ""
		}
		trimmed = replacement
	}
	return trimmed
}

// truncateFileName 把超长文件名截到 max 个 rune，保留 ≤10 字符的扩展名
// （上游 PathUtil.GetValidFileName 的截断语义：不加省略号，长度严格等于上限）。
func truncateFileName(name string, max int) string {
	runes := []rune(name)
	if len(runes) <= max {
		return name
	}
	if ext := filepath.Ext(name); ext != "" {
		extRunes := []rune(ext)
		if len(extRunes) <= 10 {
			base := runes[:len(runes)-len(extRunes)]
			if len(base) > max {
				return string(base[:max-len(extRunes)]) + ext
			}
			return name
		}
	}
	return string(runes[:max])
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
