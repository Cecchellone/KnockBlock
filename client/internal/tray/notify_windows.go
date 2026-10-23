//go:build windows

package tray

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
)

// Notifications use the tray icon that fyne.io/systray created: its hidden
// window has class "SystrayClass" and the icon has ID 100 (systray v1.12.2,
// systray_windows.go, initInstance). A balloon/toast is shown with
// Shell_NotifyIconW(NIM_MODIFY, NIF_INFO), which changes nothing else. If the
// window cannot be found (a systray change), notifications are skipped.

const (
	systrayClass  = "SystrayClass"
	systrayIconID = 100
	nimModify     = 0x1
	nifInfo       = 0x10
	niifInfo      = 0x1
	niifNoSound   = 0x10

	sndAsync     = 0x1
	sndNoDefault = 0x2
	sndAlias     = 0x10000
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	shell32           = windows.NewLazySystemDLL("shell32.dll")
	procGetClassNameW = user32.NewProc("GetClassNameW")
	procShellNotify   = shell32.NewProc("Shell_NotifyIconW")
	procPlaySoundW    = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")

	findOnce  sync.Once
	findCB    uintptr
	foundMu   sync.Mutex
	foundHwnd windows.HWND
	ourPID    = windows.GetCurrentProcessId()
	classBuf  [64]uint16
	wantClass = windows.StringToUTF16(systrayClass)
)

// notifyIconData is NOTIFYICONDATAW (shellapi.h). uTimeout and uVersion
// are a union: one field. systray declares them as two, which shifts
// szInfoTitle and dwInfoFlags by 4 bytes; it never sets them, but a
// notification needs them where Windows reads them (the title came out
// empty and the notification did not show). notify_windows_amd64.go checks
// the size.
type notifyIconData struct {
	Size                       uint32
	Wnd                        windows.Handle
	ID, Flags, CallbackMessage uint32
	Icon                       windows.Handle
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	TimeoutOrVersion           uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	GuidItem                   windows.GUID
	BalloonIcon                windows.Handle
}

// trayWindow finds this process's systray window.
func trayWindow() (windows.HWND, error) {
	findOnce.Do(func() {
		findCB = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
			var pid uint32
			if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid != ourPID {
				return 1 // continue
			}
			n, _, _ := procGetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&classBuf[0])), uintptr(len(classBuf)))
			if int(n) == len(wantClass)-1 && windows.UTF16ToString(classBuf[:n]) == systrayClass {
				foundHwnd = hwnd
				return 0 // stop
			}
			return 1
		})
	})
	foundMu.Lock()
	defer foundMu.Unlock()
	foundHwnd = 0
	windows.EnumWindows(findCB, nil) // returns an error when the callback stops early
	if foundHwnd == 0 {
		return 0, errors.New("tray: systray window not found")
	}
	return foundHwnd, nil
}

// showNotification shows a standard Windows notification from the tray
// icon, as mode says (config.Notify*): off, silent, only the Windows
// notification sound, or the notification with that sound. Windows still
// decides whether it shows (Do not disturb, notification settings).
func showNotification(mode, title, body string) error {
	switch mode {
	case config.NotifyOff:
		return nil
	case config.NotifySound:
		// The sound Windows plays for notifications (Settings > Sound).
		alias, _ := windows.UTF16PtrFromString("Notification.Default")
		if r, _, err := procPlaySoundW.Call(uintptr(unsafe.Pointer(alias)), 0, sndAlias|sndAsync|sndNoDefault); r == 0 {
			return fmt.Errorf("tray: PlaySound: %w", err)
		}
		return nil
	}
	hwnd, err := trayWindow()
	if err != nil {
		return err
	}
	nid := notifyIconData{
		Wnd:       windows.Handle(hwnd),
		ID:        systrayIconID,
		Flags:     nifInfo,
		InfoFlags: niifInfo,
	}
	if mode != config.NotifyFull {
		nid.InfoFlags |= niifNoSound
	}
	nid.Size = uint32(unsafe.Sizeof(nid))
	copyUTF16(nid.InfoTitle[:], title)
	copyUTF16(nid.Info[:], body)
	if r, _, err := procShellNotify.Call(nimModify, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return err
	}
	return nil
}

// copyUTF16 copies s into dst, truncated and NUL-terminated.
func copyUTF16(dst []uint16, s string) {
	u := windows.StringToUTF16(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}
