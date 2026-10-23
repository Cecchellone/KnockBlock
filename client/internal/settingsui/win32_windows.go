//go:build windows

package settingsui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The few Win32 calls, messages and structures the settings window needs,
// on top of golang.org/x/sys/windows (no GUI library: CLAUDE.md §2.1).

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	procAdjustWindowRectEx        = user32.NewProc("AdjustWindowRectEx")
	procAdjustWindowRectExForDpi  = user32.NewProc("AdjustWindowRectExForDpi")
	procCreateIconFromResourceEx  = user32.NewProc("CreateIconFromResourceEx")
	procCreateWindowExW           = user32.NewProc("CreateWindowExW")
	procDefWindowProcW            = user32.NewProc("DefWindowProcW")
	procDestroyIcon               = user32.NewProc("DestroyIcon")
	procDestroyWindow             = user32.NewProc("DestroyWindow")
	procDispatchMessageW          = user32.NewProc("DispatchMessageW")
	procEnableWindow              = user32.NewProc("EnableWindow")
	procEnumChildWindows          = user32.NewProc("EnumChildWindows")
	procGetDC                     = user32.NewProc("GetDC")
	procGetDpiForWindow           = user32.NewProc("GetDpiForWindow")
	procGetKeyState               = user32.NewProc("GetKeyState")
	procGetSysColor               = user32.NewProc("GetSysColor")
	procGetSysColorBrush          = user32.NewProc("GetSysColorBrush")
	procGetMessageW               = user32.NewProc("GetMessageW")
	procGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	procGetWindowRect             = user32.NewProc("GetWindowRect")
	procGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	procIsDialogMessageW          = user32.NewProc("IsDialogMessageW")
	procLoadCursorW               = user32.NewProc("LoadCursorW")
	procMessageBoxW               = user32.NewProc("MessageBoxW")
	procMonitorFromWindow         = user32.NewProc("MonitorFromWindow")
	procMoveWindow                = user32.NewProc("MoveWindow")
	procPostMessageW              = user32.NewProc("PostMessageW")
	procPostQuitMessage           = user32.NewProc("PostQuitMessage")
	procPrintWindow               = user32.NewProc("PrintWindow")
	procRegisterClassExW          = user32.NewProc("RegisterClassExW")
	procReleaseDC                 = user32.NewProc("ReleaseDC")
	procSendMessageW              = user32.NewProc("SendMessageW")
	procSetFocus                  = user32.NewProc("SetFocus")
	procSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	procSetWindowPos              = user32.NewProc("SetWindowPos")
	procSetWindowTextW            = user32.NewProc("SetWindowTextW")
	procShowWindow                = user32.NewProc("ShowWindow")
	procSystemParametersInfoForDp = user32.NewProc("SystemParametersInfoForDpi")
	procSystemParametersInfoW     = user32.NewProc("SystemParametersInfoW")
	procTranslateMessage          = user32.NewProc("TranslateMessage")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procCreateFontIndirect = gdi32.NewProc("CreateFontIndirectW")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procSetBkColor         = gdi32.NewProc("SetBkColor")

	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
)

const (
	// Window styles.
	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsClipChildren = 0x02000000
	wsDisabled     = 0x08000000

	wsExClientEdge    = 0x00000200
	wsExControlParent = 0x00010000
	wsExDlgModalFrame = 0x00000001
	wsExAppWindow     = 0x00040000

	// Control styles.
	bsPushButton    = 0x0
	bsDefPushButton = 0x1
	bsAutoCheckBox  = 0x3
	bsGroupBox      = 0x7
	bsMultiline     = 0x2000
	esMultiline     = 0x4
	esPassword      = 0x20
	esAutoVScroll   = 0x40
	esAutoHScroll   = 0x80
	esNumber        = 0x2000
	esWantReturn    = 0x1000
	cbsDropDownList = 0x3
	ssNoPrefix      = 0x80

	// Messages.
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmSetFont        = 0x0030
	wmSetIcon        = 0x0080
	wmCommand        = 0x0111
	wmCtlColorStatic = 0x0138
	wmDpiChanged     = 0x02E0
	wmApp            = 0x8000
	bmGetCheck       = 0x00F0
	bmSetCheck       = 0x00F1
	cbAddString      = 0x0143
	cbGetCurSel      = 0x0147
	cbSetCurSel      = 0x014E
	emSetSel         = 0x00B1
	emSetLimitText   = 0x00C5
	emSetCueBanner   = 0x1501

	bnClicked    = 0
	cbnSelChange = 1
	enKillFocus  = 0x0200
	idCancel     = 2

	swShow        = 5
	swRestore     = 9
	swpNoZOrder   = 0x4
	swpNoActivate = 0x10
	cwUseDefault  = 0x80000000 // CW_USEDEFAULT; only the low 32 bits are read

	mbOK          = 0x0
	mbIconWarning = 0x30

	colorWindow            = 5
	idcArrow               = 32512
	iconSmall, iconBig     = 0, 1
	spiGetNonClientMetrics = 0x29
	logPixelsY             = 90
	monitorDefaultNearest  = 2
	pwRenderFullContent    = 2
	iccStandardClasses     = 0x4000
	iccWin95Classes        = 0xFF
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

type point struct{ X, Y int32 }

type msg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type logFont struct {
	Height, Width, Escapement, Orientation, Weight       int32
	Italic, Underline, StrikeOut, CharSet                byte
	OutPrecision, ClipPrecision, Quality, PitchAndFamily byte
	FaceName                                             [32]uint16
}

type nonClientMetrics struct {
	Size                                   uint32
	BorderWidth, ScrollWidth, ScrollHeight int32
	CaptionWidth, CaptionHeight            int32
	CaptionFont                            logFont
	SmCaptionWidth, SmCaptionHeight        int32
	SmCaptionFont                          logFont
	MenuWidth, MenuHeight                  int32
	MenuFont, StatusFont, MessageFont      logFont
	PaddedBorderWidth                      int32
}

// Tab control, keyboard and visibility.
const (
	wsClipSiblings = 0x04000000
	wmNotify       = 0x004E
	wmHScroll      = 0x0114

	// Trackbar (slider) control, commctrl.h.
	tbsAutoTicks   = 0x0001
	tbsHorz        = 0x0000
	tbsBottom      = 0x0000
	tbmGetPos      = 0x0400 // WM_USER
	tbmSetPos      = 0x0405
	tbmSetRange    = 0x0406
	tbmSetTicFreq  = 0x0414
	tbmSetPageSize = 0x0415
	tbmSetLineSize = 0x0417
	tbEndTrack     = 8
	wmKeyDown      = 0x0100
	tcmGetCurSel   = 0x130B
	tcmSetCurSel   = 0x130C
	tcmInsertItem  = 0x133E
	tcifText       = 0x1
	vkTab          = 0x09
	vkShift        = 0x10
	vkControl      = 0x11
	swHide         = 0
)

type initCommonControlsEx struct{ Size, ICC uint32 }

// tcItem is TCITEMW, for TCM_INSERTITEMW.
type tcItem struct {
	Mask, State, StateMask uint32
	Text                   *uint16
	TextMax                int32
	Image                  int32
	Param                  uintptr
}

type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

func utf16Ptr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil { // s contains NUL: show nothing rather than fail
		p, _ = windows.UTF16PtrFromString("")
	}
	return p
}

func sendMessage(hwnd windows.HWND, m uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(uintptr(hwnd), uintptr(m), wParam, lParam)
	return r
}

func postMessage(hwnd windows.HWND, m uint32, wParam, lParam uintptr) {
	procPostMessageW.Call(uintptr(hwnd), uintptr(m), wParam, lParam)
}

func setText(hwnd windows.HWND, s string) {
	procSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16Ptr(s))))
}

func getText(hwnd windows.HWND) string {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

func enableWindow(hwnd windows.HWND, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	procEnableWindow.Call(uintptr(hwnd), v)
}

// mulDiv scales v from 96 DPI to dpi, rounding.
func mulDiv(v int32, dpi uint32) int32 {
	return int32((int64(v)*int64(dpi) + 48) / 96)
}
