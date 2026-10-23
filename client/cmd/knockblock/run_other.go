//go:build !windows

package main

import (
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/tray"
)

func run(bool, time.Duration) error { return tray.Run(tray.Options{Version: version}) }
