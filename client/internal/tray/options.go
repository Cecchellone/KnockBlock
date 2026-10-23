package tray

import "log/slog"

// Options configures the tray app.
type Options struct {
	ConfigPath string
	Version    string
	Logger     *slog.Logger
	// OpenSettings opens the settings UI; it is called with the current
	// config path and returns at once.
	OpenSettings func(configPath string) error
}
