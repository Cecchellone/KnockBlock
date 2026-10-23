//go:build windows

// Package endsession lets Windows close the app cleanly: when an installer
// updates KnockBlock (Windows Installer's Restart Manager) or the user signs
// out, the app reports itself offline and quits instead of being killed.
//
// It creates a hidden top-level window, the kind Restart Manager and the
// shutdown sequence send WM_QUERYENDSESSION / WM_ENDSESSION to (systray's own
// window belongs to a library and cannot be hooked), and registers the app
// for restart, so Restart Manager starts it again after the update.
package endsession

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                         = windows.NewLazySystemDLL("user32.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procRegisterApplicationRestart = kernel32.NewProc("RegisterApplicationRestart")
)

const (
	wmClose            = 0x0010
	wmQueryEndSession  = 0x0011
	wmEndSession       = 0x0016
	endSessionCloseApp = 0x1 // lParam: Restart Manager closes the app (update)

	restartNoCrash  = 0x1
	restartNoHang   = 0x2
	restartNoReboot = 0x8
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type msg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

var (
	once    sync.Once
	onClose func(update bool)
)

// Watch calls close when Windows asks the app to end: update is true when
// an installer is replacing it (Restart Manager, or a WM_CLOSE from the
// installer's CloseApplication), false when the user signs out or shuts
// down. close runs on the watcher's thread and should return once the app
// has said goodbye; the window answers Windows only after it returns. It
// also registers the app to be restarted after an update. Call it once.
func Watch(close func(update bool)) error {
	var err error
	once.Do(func() { err = start(close) })
	return err
}

func start(close func(update bool)) error {
	// Restart Manager restarts registered apps it closed for an update; not
	// after a crash, a hang or a reboot.
	if r, _, err := procRegisterApplicationRestart.Call(0, restartNoCrash|restartNoHang|restartNoReboot); r != 0 {
		return fmt.Errorf("endsession: RegisterApplicationRestart: %w", err)
	}
	onClose = close
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // the window's messages arrive on this thread
		var inst windows.Handle
		if err := windows.GetModuleHandleEx(0, nil, &inst); err != nil {
			ready <- err
			return
		}
		name, _ := windows.UTF16PtrFromString("KnockBlockEndSession")
		wc := wndClassEx{WndProc: windows.NewCallback(wndProc), Instance: inst, ClassName: name}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			ready <- fmt.Errorf("endsession: RegisterClassEx: %w", err)
			return
		}
		// Top-level and never shown: message-only windows do not get the
		// session messages.
		h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)),
			0, 0, 0, 0, 0, 0, 0, uintptr(inst), 0)
		if h == 0 {
			ready <- fmt.Errorf("endsession: CreateWindowEx: %w", err)
			return
		}
		ready <- nil
		var m msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	return <-ready
}

var closing sync.Once

func wndProc(hwnd windows.HWND, m, wParam, lParam uintptr) uintptr {
	switch m {
	case wmQueryEndSession:
		return 1 // yes, we can close
	case wmEndSession:
		if wParam != 0 {
			closing.Do(func() { onClose(lParam&endSessionCloseApp != 0) })
		}
		return 0
	case wmClose:
		// Sent by an installer that closes running copies before replacing
		// them; the window itself is never closed otherwise.
		closing.Do(func() { onClose(true) })
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), m, wParam, lParam)
	return r
}
