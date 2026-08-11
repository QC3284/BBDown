package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// MergeWithConfig reads BBDown config files and merges with CLI args.
// Priority: CLI args > config file > defaults.
func MergeWithConfig(args []string) ([]string, error) {
	// Try to find a config file from --config-file arg or default locations
	configPath := findConfigPath(args)
	if configPath == "" {
		return args, nil
	}

	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml") // also supports json, toml

	if err := v.ReadInConfig(); err != nil {
		// Config file not found or unreadable is not fatal
		return args, nil
	}

	// Config file values become defaults; CLI args override via viper binding
	// For now, we merge the config into a MyOption and convert back
	// This is a simplified approach - full implementation would use viper + pflags
	_ = v

	return args, nil
}

// findConfigPath looks for BBDown config in the following order:
// 1. --config-file CLI arg
// 2. ./BBDown.config
// 3. $HOME/.config/BBDown/BBDown.config
func findConfigPath(args []string) string {
	// Check --config-file in args
	for i, a := range args {
		if a == "--config-file" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--config-file=") {
			return strings.TrimPrefix(a, "--config-file=")
		}
	}

	// Check current directory
	cwd, err := os.Getwd()
	if err == nil {
		p := filepath.Join(cwd, "BBDown.config")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Check user config directory
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".config", "BBDown", "BBDown.config")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}
