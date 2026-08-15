package config

import (
	"context"
	"sync"
)

// contextKey is the type for config context keys.
type contextKey string

const settingsKey contextKey = "bbdown-settings"

var (
	globalMu       sync.RWMutex
	globalSettings AppSettings
)

func init() {
	globalSettings = DefaultAppSettings()
}

// Current returns the current effective AppSettings.
// If a task-local settings snapshot is available in ctx, it is returned;
// otherwise the global settings are returned.
func Current(ctx context.Context) AppSettings {
	if ctx != nil {
		if s, ok := ctx.Value(settingsKey).(AppSettings); ok {
			return s
		}
	}
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalSettings
}

// Apply updates the global settings (and task-local if ctx carries a snapshot).
func Apply(settings AppSettings) {
	globalMu.Lock()
	globalSettings = settings
	globalMu.Unlock()
}

// WithSettings attaches a settings snapshot to a context for task-local isolation.
func WithSettings(ctx context.Context, s AppSettings) context.Context {
	return context.WithValue(ctx, settingsKey, s)
}

// SettingsFrom returns the task-local settings snapshot from ctx, or the current global defaults.
func SettingsFrom(ctx context.Context) AppSettings {
	return Current(ctx)
}

// Convenience accessors for commonly accessed settings.

func Cookie(ctx context.Context) string     { return Current(ctx).Cookie }
func Token(ctx context.Context) string      { return Current(ctx).Token }
func DebugLog(ctx context.Context) bool     { return Current(ctx).DebugLog }
func Host(ctx context.Context) string       { return Current(ctx).Host }
func EpHost(ctx context.Context) string     { return Current(ctx).EpHost }
func TvHost(ctx context.Context) string     { return Current(ctx).TvHost }
func Area(ctx context.Context) string       { return Current(ctx).Area }
func Wbi(ctx context.Context) string        { return Current(ctx).Wbi }
func SkipSslCheck(ctx context.Context) bool { return Current(ctx).SkipSslCheck }
