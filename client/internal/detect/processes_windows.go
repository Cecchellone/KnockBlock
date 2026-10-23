//go:build windows

package detect

import (
	"slices"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winProcesses lists running programs with a Toolhelp snapshot and reads
// window titles with EnumWindows. It runs only while an app is in use.
type winProcesses struct {
	pids map[string][]uint32 // lower-case executable name → process IDs
}

func (p *winProcesses) refresh() error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	if p.pids == nil {
		p.pids = make(map[string][]uint32)
	}
	for k, v := range p.pids {
		p.pids[k] = v[:0]
	}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		p.pids[name] = append(p.pids[name], e.ProcessID)
	}
	return nil
}

func (p *winProcesses) running(exe string) bool { return len(p.pids[exe]) > 0 }

var procGetWindowTextW = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowTextW")

// One EnumWindows callback serves every call; enumMu guards its state.
var (
	enumOnce  sync.Once
	enumCB    uintptr
	enumMu    sync.Mutex
	enumPIDs  []uint32
	enumFn    func(string) bool
	enumTitle [512]uint16
)

func (p *winProcesses) titles(exe string, fn func(title string) bool) {
	pids := p.pids[exe]
	if len(pids) == 0 {
		return
	}
	enumOnce.Do(func() {
		enumCB = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
			if !windows.IsWindowVisible(hwnd) {
				return 1
			}
			var pid uint32
			if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || !slices.Contains(enumPIDs, pid) {
				return 1
			}
			n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&enumTitle[0])), uintptr(len(enumTitle)))
			if n == 0 {
				return 1
			}
			if !enumFn(windows.UTF16ToString(enumTitle[:n])) {
				return 0 // stop
			}
			return 1
		})
	})
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPIDs, enumFn = pids, fn
	windows.EnumWindows(enumCB, nil) // returns an error when stopped early
	enumPIDs, enumFn = nil, nil
}
