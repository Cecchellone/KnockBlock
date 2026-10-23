// Command knockblock is the KnockBlock Windows tray app: it detects
// microphone and webcam usage and sends the resulting status to the
// KnockBlock device over the ESPHome Native API.
//
// This is a placeholder entry point; the tray app is built in milestone 6
// (see CLAUDE.md §7).
package main

import "fmt"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Println("KnockBlock", version)
}
