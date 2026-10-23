//go:build !windows

package tray

import "errors"

// Run is only implemented on Windows (CLAUDE.md §10: no macOS/Linux tray).
func Run(Options) error {
	return errors.New("tray: the KnockBlock tray app runs on Windows only; use knockctl agent elsewhere")
}
