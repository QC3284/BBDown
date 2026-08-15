package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SubCommandNames are the registered subcommands: their flags do not support
// the full download option set, so config merging is skipped for them.
var SubCommandNames = []string{"login", "logintv", "serve", "live", "article", "watchlater", "sub"}

var urlLikeToken = regexp.MustCompile(`(?i)^(https?://|av\d+|bv[0-9A-Za-z]+|av:|bv:|ep\d+|ep:|ss\d+|ss:|md\d+|md:|cheese[:/]|mid:|favId:|listBizId:|seriesBizId:)`)

// IsSubCommandInvocation reports whether the first positional token is a
// subcommand name (value-consuming options are skipped while scanning).
func IsSubCommandInvocation(args []string, aliasMap map[string]string, boolFlags map[string]bool) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			for _, name := range SubCommandNames {
				if strings.EqualFold(arg, name) {
					return true
				}
			}
			return false
		}
		if strings.Contains(arg, "=") {
			continue // value embedded in the token
		}
		if canonical, ok := aliasMap[arg]; ok && !boolFlags[canonical] {
			i++ // this option consumes the next token as its value
		}
	}
	return false
}

// MergeWithConfig merges a line-based BBDown.config file into the CLI args
// (upstream BBDownConfigParser): config options act as defaults, explicit CLI
// options win, and the config URL is dropped when the CLI already has one.
func MergeWithConfig(cliArgs []string, aliasMap map[string]string, boolFlags map[string]bool) ([]string, error) {
	result := append([]string(nil), cliArgs...)

	if IsSubCommandInvocation(cliArgs, aliasMap, boolFlags) {
		return result, nil
	}

	configPath := ""
	for i := 0; i < len(cliArgs); i++ {
		if cliArgs[i] == "--config-file" && i+1 < len(cliArgs) {
			configPath = cliArgs[i+1]
			break
		}
		if strings.HasPrefix(cliArgs[i], "--config-file=") {
			configPath = strings.TrimPrefix(cliArgs[i], "--config-file=")
			break
		}
	}
	if configPath == "" {
		configPath = filepath.Join(appDir(), "BBDown.config")
	}

	if _, err := os.Stat(configPath); err != nil {
		return result, nil
	}

	lines, err := os.ReadFile(configPath)
	if err != nil {
		return result, nil
	}
	var configArgs []string
	for _, line := range strings.Split(string(lines), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "-") && strings.Contains(line, " ") {
			idx := strings.Index(line, " ")
			configArgs = append(configArgs, line[:idx], strings.Trim(strings.TrimSpace(line[idx:]), "\""))
		} else {
			configArgs = append(configArgs, strings.Trim(line, "\""))
		}
	}

	cliHasURL := false
	for _, a := range cliArgs {
		if urlLikeToken.MatchString(a) {
			cliHasURL = true
			break
		}
	}

	explicitOptions := make(map[string]bool)
	for _, a := range cliArgs {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		token := a
		if idx := strings.Index(a, "="); idx > 0 {
			token = a[:idx]
		}
		if canonical, ok := aliasMap[token]; ok {
			explicitOptions[canonical] = true
		}
	}

	for i := 0; i < len(configArgs); {
		name := configArgs[i]
		if !strings.HasPrefix(name, "-") {
			if !cliHasURL {
				result = append(result, name)
			}
			i++
			continue
		}

		canonical, known := aliasMap[name]
		if !known {
			result = append(result, name)
			i++
			continue
		}

		if explicitOptions[canonical] {
			i++
			// Skip the config value(s) for this option: collect tokens until the
			// next known option name.
			for i < len(configArgs) && (!strings.HasPrefix(configArgs[i], "-") || !isKnownOption(configArgs[i], aliasMap)) {
				i++
			}
			continue
		}

		result = append(result, name)
		i++
		for i < len(configArgs) && (!strings.HasPrefix(configArgs[i], "-") || !isKnownOption(configArgs[i], aliasMap)) {
			result = append(result, configArgs[i])
			i++
		}
	}

	return result, nil
}

func isKnownOption(token string, aliasMap map[string]string) bool {
	_, ok := aliasMap[token]
	return ok
}

// appDir returns the executable directory (config default location).
func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
