//go:build !windows && !linux

package memdebug

func processMemory() (process, bool) { return process{}, false }
