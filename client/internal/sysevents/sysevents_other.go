//go:build !windows

package sysevents

// Watch does nothing off Windows; the connection's keepalive still notices
// a dead link.
func Watch(func(reason string)) (stop func(), err error) { return func() {}, nil }
