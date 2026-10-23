// Command knockblock is the KnockBlock Windows tray app: it detects
// microphone and webcam use and reports this PC's status to the KnockBlock
// light over the ESPHome Native API (CLAUDE.md §5).
//
//	knockblock [-debug] [-debug-mem 30s]
//
// It logs to %LOCALAPPDATA%\KnockBlock\logs\knockblock.log and reads
// %APPDATA%\KnockBlock\config.json.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "debug logging")
	debugMem := flag.Duration("debug-mem", 0, "log memory statistics at this interval (0 = off)")
	flag.Parse()
	if err := run(*debug, *debugMem); err != nil {
		fmt.Fprintln(os.Stderr, "knockblock:", err)
		os.Exit(1)
	}
}
