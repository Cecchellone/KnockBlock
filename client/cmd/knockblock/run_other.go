//go:build !windows

package main

import "github.com/Cecchellone/KnockBlock/client/internal/tray"

func run(options) error { return tray.Run(tray.Options{Version: version}) }
