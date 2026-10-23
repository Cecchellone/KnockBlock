//go:build !windows

package autostart

import "errors"

// Enabled reports false: autostart is only implemented on Windows.
func Enabled() bool { return false }

// Set is only implemented on Windows.
func Set(bool) error { return errors.New("autostart: only supported on Windows") }
