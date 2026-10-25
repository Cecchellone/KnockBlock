//go:build windows

package idle

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procOpenInputDesktop = user32.NewProc("OpenInputDesktop")
	procCloseDesktop     = user32.NewProc("CloseDesktop")
	procGetTickCount     = kernel32.NewProc("GetTickCount")
)

const desktopSwitchDesktop = 0x0100 // DESKTOP_SWITCHDESKTOP

// Away reports whether the session is locked (the input desktop is not
// ours: the lock screen) or had no keyboard or mouse input for at least
// after. Both calls are cheap enough for every poll.
func Away(after time.Duration) bool {
	// While locked, OpenInputDesktop with DESKTOP_SWITCHDESKTOP fails: the
	// input desktop is Winlogon's.
	if h, _, _ := procOpenInputDesktop.Call(0, 0, desktopSwitchDesktop); h == 0 {
		return true
	} else {
		procCloseDesktop.Call(h)
	}
	info := struct{ size, time uint32 }{size: 8}
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return false
	}
	now, _, _ := procGetTickCount.Call()
	return time.Duration(uint32(now)-info.time)*time.Millisecond >= after
}
