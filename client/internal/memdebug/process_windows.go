//go:build windows

package memdebug

import (
	"syscall"
	"unsafe"
)

// processMemoryCountersEx2 is PROCESS_MEMORY_COUNTERS_EX2 (psapi.h,
// Windows 10 1809+), which adds the private working set shown by Task
// Manager.
type processMemoryCountersEx2 struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
	privateUsage               uintptr
	privateWorkingSetSize      uintptr
	sharedCommitUsage          uint64
}

var procGetProcessMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func processMemory() (process, bool) {
	var c processMemoryCountersEx2
	c.cb = uint32(unsafe.Sizeof(c))
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return process{}, false
	}
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r == 0 {
		return process{}, false
	}
	return process{
		workingSet:        uint64(c.workingSetSize),
		privateWorkingSet: uint64(c.privateWorkingSetSize),
		commit:            uint64(c.privateUsage),
	}, true
}
