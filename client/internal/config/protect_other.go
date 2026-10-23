//go:build !windows

package config

// protect is a no-op off Windows: the tray app only runs on Windows, and
// tests need a working round trip. The stored value is merely base64.
func protect(data []byte) ([]byte, error) { return append([]byte(nil), data...), nil }

func unprotect(data []byte) ([]byte, error) { return append([]byte(nil), data...), nil }
