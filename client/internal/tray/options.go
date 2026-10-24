package tray

import (
	"log/slog"

	"github.com/Cecchellone/KnockBlock/client/internal/discord"
)

// Options configures the tray app.
type Options struct {
	ConfigPath string
	Version    string
	Logger     *slog.Logger
	// OpenSettings opens the settings UI; it is called with the current
	// config path and returns at once.
	OpenSettings func(configPath string) error
	// Discord follows Discord's mute button when the settings turn it on;
	// nil leaves it out.
	Discord *discord.Client
}
