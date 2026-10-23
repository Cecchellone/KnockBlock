//go:build !windows

// Package endsession lets Windows close the app cleanly for an update or a
// sign-out; elsewhere it does nothing.
package endsession

import "errors"

// ErrUnsupported is returned where there is nothing to watch.
var ErrUnsupported = errors.New("endsession: not supported on this platform")

// Watch returns ErrUnsupported.
func Watch(func(update bool)) error { return ErrUnsupported }
