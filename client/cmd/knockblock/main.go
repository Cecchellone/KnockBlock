// Command knockblock is the KnockBlock Windows tray app: it detects
// microphone and webcam use and reports this PC's status to the KnockBlock
// light over the ESPHome Native API (CLAUDE.md §5).
//
//	knockblock [-debug] [-debug-mem 30s]
//
// -settings-smoke FILE is for the release smoke test: it opens the settings
// window alone, saves a PNG screenshot of it to FILE, closes it and exits.
//
// It logs to %LOCALAPPDATA%\KnockBlock\logs\knockblock.log and reads
// %APPDATA%\KnockBlock\config.json.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

type options struct {
	debug         bool
	debugMem      time.Duration
	settingsSmoke string
}

func main() {
	var o options
	flag.BoolVar(&o.debug, "debug", false, "debug logging")
	flag.DurationVar(&o.debugMem, "debug-mem", 0, "log memory statistics at this interval (0 = off)")
	flag.StringVar(&o.settingsSmoke, "settings-smoke", "", "open the settings window, save a screenshot to this PNG file, and exit")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "knockblock:", err)
		os.Exit(1)
	}
}
