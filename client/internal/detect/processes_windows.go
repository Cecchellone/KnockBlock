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
	pids  map[string][]uint32 // lower-case executable name → process IDs
	exes  map[uint32]string   // process ID → lower-case executable name
	paths map[uint32]string   // process ID → full path, read on demand
	buf   [windows.MAX_LONG_PATH]uint16
}

func (p *winProcesses) refresh() error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	if p.pids == nil {
		p.pids = make(map[string][]uint32)
		p.exes = make(map[uint32]string)
		p.paths = make(map[uint32]string)
	}
	for k, v := range p.pids {
		p.pids[k] = v[:0]
	}
	clear(p.exes)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		p.pids[name] = append(p.pids[name], e.ProcessID)
		p.exes[e.ProcessID] = name
	}
	for pid := range p.paths {
		if _, ok := p.exes[pid]; !ok {
			delete(p.paths, pid) // exited; the ID may be reused
		}
	}
	return nil
}

func (p *winProcesses) running(exe, path string) bool {
	pids := p.pids[exe]
	if len(pids) == 0 {
		return false
	}
	if path == "" {
		return true
	}
	for _, pid := range pids {
		got := p.path(pid)
		if got == "" || strings.EqualFold(got, path) {
			return true // a path Windows does not tell cannot rule it out
		}
	}
	return false
}

func (p *winProcesses) process(pid uint32) (exe, path string, ok bool) {
	exe, ok = p.exes[pid]
	if !ok {
		return "", "", false
	}
	return exe, p.path(pid), true
}

// path returns the full executable path of a process, or "" when it cannot
// be read (a process of another user, or elevated).
func (p *winProcesses) path(pid uint32) string {
	if path, ok := p.paths[pid]; ok {
		return path
	}
	var path string
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		n := uint32(len(p.buf))
		if windows.QueryFullProcessImageName(h, 0, &p.buf[0], &n) == nil {
			path = windows.UTF16ToString(p.buf[:n])
		}
		windows.CloseHandle(h)
	}
	p.paths[pid] = path
	return path
}

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
