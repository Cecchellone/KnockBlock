//go:build linux

package memdebug

import (
	"bytes"
	"os"
	"strconv"
)

// processMemory reads the resident set size from /proc/self/statm.
func processMemory() (process, bool) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return process{}, false
	}
	fields := bytes.Fields(data)
	if len(fields) < 2 {
		return process{}, false
	}
	pages, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return process{}, false
	}
	return process{workingSet: pages * uint64(os.Getpagesize())}, true
}
