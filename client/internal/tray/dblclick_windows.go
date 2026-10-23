//go:build windows

package tray

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

// fyne.io/systray opens the menu on a left click and does not report double
// clicks. watchDoubleClick subclasses systray's window (trayWindow) so that
// a double click opens the settings: a left click is held back for the
// double-click time and only then passed on to systray, which opens the
// menu; a double click cancels it. The tray icon reports mouse events with
// systray's callback message, WM_USER+1, and the event in lParam
// (systray v1.12.2, systray_windows.go, initInstance and wndProc).

const (
	wmTimer          = 0x0113
	wmLButtonUp      = 0x0202
	wmLButtonDblClk  = 0x0203
	systrayCallback  = 0x0400 + 1  // WM_USER + 1
	gwlpWndProc      = ^uintptr(3) // GWLP_WNDPROC, -4
	doubleClickTimer = 0x4B42      // our timer ID on systray's window
)

var (
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW   = user32.NewProc("CallWindowProcW")
	procSetTimer          = user32.NewProc("SetTimer")
	procKillTimer         = user32.NewProc("KillTimer")
	procGetDoubleClickTim = user32.NewProc("GetDoubleClickTime")

	dbl struct {
		once     sync.Once
		orig     uintptr // systray's window procedure
		onDouble func()
		swallow  bool // the button-up that ends a double click
	}
)

// watchDoubleClick calls onDouble, on its own goroutine, when the tray icon
// is double-clicked. Call it once systray is ready.
func watchDoubleClick(onDouble func()) error {
	hwnd, err := trayWindow()
	if err != nil {
		return err
	}
	dbl.once.Do(func() {
		dbl.onDouble = onDouble
		// Read systray's procedure before replacing it: messages may arrive
		// as soon as it is replaced.
		if dbl.orig, _, err = procGetWindowLongPtrW.Call(uintptr(hwnd), gwlpWndProc); dbl.orig == 0 {
			err = fmt.Errorf("tray: GetWindowLongPtr: %w", err)
			return
		}
		if r, _, e := procSetWindowLongPtrW.Call(uintptr(hwnd), gwlpWndProc, windows.NewCallback(subclassProc)); r == 0 {
			err = fmt.Errorf("tray: SetWindowLongPtr: %w", e)
			return
		}
		err = nil
	})
	return err
}

// subclassProc runs on systray's window thread, like systray's own
// procedure, so dbl needs no lock beyond that.
func subclassProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch {
	case msg == systrayCallback && lParam == wmLButtonUp:
		if dbl.swallow {
			dbl.swallow = false
			return 0
		}
		ms, _, _ := procGetDoubleClickTim.Call()
		procSetTimer.Call(uintptr(hwnd), doubleClickTimer, ms, 0)
		return 0
	case msg == systrayCallback && lParam == wmLButtonDblClk:
		procKillTimer.Call(uintptr(hwnd), doubleClickTimer)
		dbl.swallow = true // a button-up follows the double click
		go dbl.onDouble()
		return 0
	case msg == wmTimer && wParam == doubleClickTimer:
		procKillTimer.Call(uintptr(hwnd), doubleClickTimer)
		// A single click after all: let systray open the menu.
		r, _, _ := procCallWindowProcW.Call(dbl.orig, uintptr(hwnd), systrayCallback, 0, wmLButtonUp)
		return r
	}
	r, _, _ := procCallWindowProcW.Call(dbl.orig, uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}
