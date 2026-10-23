//go:build windows

package settingsui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Cecchellone/KnockBlock/client/internal/icon"
)

// Window is the native settings window. It exists only while open: Open
// starts a thread with its own message loop, and closing the window ends
// it (CLAUDE.md §2.1).
type Window struct {
	deps Deps

	mu  sync.Mutex
	cur *win
}

// NewWindow returns a closed settings window.
func NewWindow(d Deps) *Window {
	d.defaults()
	return &Window{deps: d}
}

// Open shows the window, or brings it to the front if it is already open.
// It returns once the window is on screen.
func (w *Window) Open() error {
	w.mu.Lock()
	if c := w.cur; c != nil {
		w.mu.Unlock()
		procShowWindow.Call(uintptr(c.hwnd), swRestore)
		procSetForegroundWindow.Call(uintptr(c.hwnd))
		return nil
	}
	c := &win{model: Load(w.deps), ready: make(chan error, 1), done: make(chan struct{})}
	w.cur = c
	w.mu.Unlock()

	go c.run()
	if err := <-c.ready; err != nil {
		w.mu.Lock()
		w.cur = nil
		w.mu.Unlock()
		return err
	}
	go func() {
		<-c.done
		w.mu.Lock()
		if w.cur == c {
			w.cur = nil
		}
		w.mu.Unlock()
	}()
	return nil
}

// Close closes the window, if open, and waits until it is gone.
func (w *Window) Close() {
	w.mu.Lock()
	c := w.cur
	w.mu.Unlock()
	if c != nil {
		postMessage(c.hwnd, wmClose, 0, 0)
		<-c.done
	}
}

// Capture writes a PNG screenshot of the open window and returns how many
// controls it has. The release smoke test uses it to check the window on a
// real Windows.
func (w *Window) Capture(out io.Writer) (controls int, err error) {
	w.mu.Lock()
	c := w.cur
	w.mu.Unlock()
	if c == nil {
		return 0, errors.New("settingsui: window not open")
	}
	enumMu.Lock()
	enumCount = 0
	procEnumChildWindows.Call(uintptr(c.hwnd), enumCallback(), 0)
	controls = enumCount
	enumMu.Unlock()
	img, err := capture(c.hwnd)
	if err != nil {
		return controls, err
	}
	return controls, png.Encode(out, img)
}

// Window IDs of the controls that send commands.
const (
	idSave = idOK
	idTest = 100
)

// wmTestDone tells the window thread that the connection test finished.
const wmTestDone = wmApp + 1

// box is a control's place in 96-DPI client coordinates.
type box struct{ X, Y, W, H int32 }

type control struct {
	hwnd windows.HWND
	box  box
}

// win is one open window. Every field but the test result belongs to the
// window thread.
type win struct {
	model *Model
	ready chan error
	done  chan struct{}

	hwnd  windows.HWND
	inst  windows.Handle
	dpi   uint32
	font  uintptr
	icons [2]uintptr
	ctrls []control

	host, port, key, removeKey, testBtn, result  windows.HWND
	displayLang, flip, pcName, lang              windows.HWND
	sendApps, doorbell, notifications, autostart windows.HWND
	ignore, names                                windows.HWND
	langChoices, displayChoices, flipChoices     []Choice
	cancelTest                                   context.CancelFunc

	testMu  sync.Mutex
	testMsg string
}

// Client area size, in 96-DPI units.
const clientW, clientH = 636, 556

// A fixed-size window with a title bar, close and minimize buttons, and a
// taskbar button.
const (
	windowStyle   = wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren
	windowExStyle = wsExControlParent | wsExAppWindow
)

var (
	registerOnce sync.Once
	registerErr  error
	className    = utf16Ptr("KnockBlockSettings")

	// byHwnd maps open window handles to their state, for the window
	// procedure (one callback serves every window).
	windowsMu sync.Mutex
	byHwnd    = map[windows.HWND]*win{}

	wndProcOnce sync.Once
	wndProcCB   uintptr

	enumOnce  sync.Once
	enumCB    uintptr
	enumMu    sync.Mutex
	enumCount int
)

func enumCallback() uintptr {
	enumOnce.Do(func() {
		enumCB = windows.NewCallback(func(windows.HWND, uintptr) uintptr {
			enumCount++
			return 1
		})
	})
	return enumCB
}

func (c *win) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.done)
	if err := c.create(); err != nil {
		if c.hwnd != 0 {
			procDestroyWindow.Call(uintptr(c.hwnd))
		}
		c.ready <- err
		return
	}
	c.ready <- nil
	if c.model.LoadErr != nil {
		c.message(c.model.SaveError(c.model.LoadErr))
	}
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // WM_QUIT or error
			return
		}
		// Tab, Enter (Save) and Esc (Cancel) as in a dialog box.
		if r, _, _ := procIsDialogMessageW.Call(uintptr(c.hwnd), uintptr(unsafe.Pointer(&m))); r != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func register(inst windows.Handle) error {
	registerOnce.Do(func() {
		wndProcOnce.Do(func() { wndProcCB = windows.NewCallback(wndProc) })
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		wc := wndClassEx{
			WndProc:    wndProcCB,
			Instance:   inst,
			Cursor:     windows.Handle(cursor),
			Background: windows.Handle(colorBtnFace + 1),
			ClassName:  className,
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			registerErr = fmt.Errorf("settingsui: RegisterClassEx: %w", err)
		}
	})
	return registerErr
}

func (c *win) create() error {
	icc := initCommonControlsEx{ICC: iccStandardClasses | iccWin95Classes}
	icc.Size = uint32(unsafe.Sizeof(icc))
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	if err := windows.GetModuleHandleEx(0, nil, &c.inst); err != nil {
		return fmt.Errorf("settingsui: %w", err)
	}
	if err := register(c.inst); err != nil {
		return err
	}
	h, _, err := procCreateWindowExW.Call(windowExStyle,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(utf16Ptr(c.model.T("settings.title")))),
		windowStyle, cwUseDefault, cwUseDefault, 100, 100, 0, 0, uintptr(c.inst), 0)
	if h == 0 {
		return fmt.Errorf("settingsui: CreateWindowEx: %w", err)
	}
	c.hwnd = windows.HWND(h)
	windowsMu.Lock()
	byHwnd[c.hwnd] = c
	windowsMu.Unlock()

	c.dpi = windowDPI(c.hwnd)
	c.font = messageFont(c.dpi)
	c.setIcons()
	c.build()
	c.fill(c.model.Form())
	c.layout()
	c.center()
	procShowWindow.Call(uintptr(c.hwnd), swShow)
	procSetForegroundWindow.Call(uintptr(c.hwnd))
	procSetFocus.Call(uintptr(c.host))
	return nil
}

// build creates the controls. Their order is the Tab order.
func (c *win) build() {
	t := c.model.T
	label := func(text string, b box) { c.add("STATIC", text, ssNoPrefix, 0, 0, b) }
	edit := func(style uint32, b box) windows.HWND {
		return c.add("EDIT", "", wsTabStop|esAutoHScroll|style, wsExClientEdge, 0, b)
	}
	combo := func(choices []Choice, b box) windows.HWND {
		b.H = 240 // height of the open list
		h := c.add("COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDownList, 0, 0, b)
		for _, ch := range choices {
			sendMessage(h, cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(ch.Label))))
		}
		return h
	}
	check := func(text string, b box) windows.HWND {
		return c.add("BUTTON", text, wsTabStop|bsAutoCheckBox|bsMultiline, 0, 0, b)
	}
	group := func(text string, b box) { c.add("BUTTON", text, bsGroupBox, 0, 0, b) }

	// The light (left column).
	const lx, iw = 24, 276
	group(t("settings.device"), box{12, 12, 300, 314})
	y := int32(34)
	label(t("settings.host"), box{lx, y, 188, 16})
	label(t("settings.port"), box{lx + 196, y, 80, 16})
	c.host = edit(0, box{lx, y + 18, 188, 23})
	sendMessage(c.host, emSetCueBanner, 0, uintptr(unsafe.Pointer(utf16Ptr("knockblock.local"))))
	c.port = edit(esNumber, box{lx + 196, y + 18, 80, 23})
	sendMessage(c.port, emSetLimitText, 5, 0)
	y += 49
	label(t("settings.key"), box{lx, y, iw, 16})
	c.key = edit(esPassword, box{lx, y + 18, iw, 23})
	if c.model.KeySaved() {
		sendMessage(c.key, emSetCueBanner, 0, uintptr(unsafe.Pointer(utf16Ptr(t("settings.key_saved")))))
	}
	y += 49
	c.removeKey = check(t("settings.key_remove"), box{lx, y, iw, 20})
	enableWindow(c.removeKey, c.model.KeySaved())
	y += 26
	c.testBtn = c.add("BUTTON", t("settings.test"), wsTabStop|bsPushButton, 0, idTest, box{lx, y, 140, 26})
	y += 30
	c.result = c.add("STATIC", "", ssNoPrefix, 0, 0, box{lx, y, iw, 32})
	y += 36
	label(t("settings.display_language"), box{lx, y, iw, 16})
	c.displayChoices = c.model.Languages("settings.display_language_keep")
	c.displayLang = combo(c.displayChoices, box{lx, y + 18, iw, 23})
	y += 49
	label(t("settings.display_flip"), box{lx, y, iw, 16})
	c.flipChoices = c.model.Orientations()
	c.flip = combo(c.flipChoices, box{lx, y + 18, iw, 23})

	// This PC (right column).
	const rx = 336
	group(t("settings.this_pc"), box{324, 12, 300, 314})
	y = 34
	label(t("settings.pc_name"), box{rx, y, iw, 16})
	c.pcName = edit(0, box{rx, y + 18, iw, 23})
	sendMessage(c.pcName, emSetCueBanner, 0, uintptr(unsafe.Pointer(utf16Ptr(c.model.DefaultPCName()))))
	sendMessage(c.pcName, emSetLimitText, 32, 0)
	y += 45
	label(t("settings.pc_name_help", "default", c.model.DefaultPCName()), box{rx, y, iw, 30})
	y += 36
	label(t("settings.language"), box{rx, y, iw, 16})
	c.langChoices = c.model.Languages("settings.language_auto")
	c.lang = combo(c.langChoices, box{rx, y + 18, iw, 23})
	y += 51
	c.sendApps = check(t("settings.send_apps"), box{rx, y, iw, 32})
	y += 34
	c.doorbell = check(t("settings.doorbell"), box{rx, y, iw, 32})
	y += 34
	c.notifications = check(t("settings.notifications"), box{rx, y, iw, 32})
	y += 34
	c.autostart = check(t("settings.autostart"), box{rx, y, iw, 32})

	// Apps (full width).
	group(t("settings.apps"), box{12, 336, 612, 170})
	y = 358
	label(t("settings.ignore"), box{24, y, 288, 16})
	label(t("settings.names"), box{324, y, 288, 16})
	multi := wsTabStop | wsVScroll | esMultiline | esAutoVScroll | esWantReturn
	c.ignore = c.add("EDIT", "", uint32(multi), wsExClientEdge, 0, box{24, y + 18, 288, 72})
	c.names = c.add("EDIT", "", uint32(multi), wsExClientEdge, 0, box{324, y + 18, 288, 72})
	label(t("settings.ignore_help"), box{24, y + 94, 288, 46})
	label(t("settings.names_help"), box{324, y + 94, 288, 46})

	// Buttons.
	c.add("BUTTON", t("settings.save"), wsTabStop|bsDefPushButton, 0, idSave, box{clientW - 12 - 208, 518, 100, 26})
	c.add("BUTTON", t("settings.cancel"), wsTabStop|bsPushButton, 0, idCancel, box{clientW - 12 - 100, 518, 100, 26})
}

// add creates a child control; layout places it.
func (c *win) add(class, text string, style, exStyle uint32, id int, b box) windows.HWND {
	h, _, _ := procCreateWindowExW.Call(uintptr(exStyle),
		uintptr(unsafe.Pointer(utf16Ptr(class))), uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(style|wsChild|wsVisible), 0, 0, 0, 0, uintptr(c.hwnd), uintptr(id), uintptr(c.inst), 0)
	hw := windows.HWND(h)
	sendMessage(hw, wmSetFont, c.font, 0)
	c.ctrls = append(c.ctrls, control{hw, b})
	return hw
}

// layout places every control and sizes the window for the current DPI.
func (c *win) layout() {
	for _, ct := range c.ctrls {
		b := ct.box
		procMoveWindow.Call(uintptr(ct.hwnd), uintptr(mulDiv(b.X, c.dpi)), uintptr(mulDiv(b.Y, c.dpi)),
			uintptr(mulDiv(b.W, c.dpi)), uintptr(mulDiv(b.H, c.dpi)), 1)
	}
}

// center sizes the window and puts it in the middle of its monitor.
func (c *win) center() {
	w, h := c.windowSize()
	mon, _, _ := procMonitorFromWindow.Call(uintptr(c.hwnd), monitorDefaultNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	x, y := int32(100), int32(100)
	if r, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r != 0 {
		x = mi.Work.Left + max(0, (mi.Work.Right-mi.Work.Left-w)/2)
		y = mi.Work.Top + max(0, (mi.Work.Bottom-mi.Work.Top-h)/2)
	}
	procSetWindowPos.Call(uintptr(c.hwnd), 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
}

// windowSize is the outer size of the window for the current DPI.
func (c *win) windowSize() (w, h int32) {
	r := rect{Right: mulDiv(clientW, c.dpi), Bottom: mulDiv(clientH, c.dpi)}
	if procAdjustWindowRectExForDpi.Find() == nil {
		procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&r)), windowStyle, 0, windowExStyle, uintptr(c.dpi))
	} else {
		procAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), windowStyle, 0, windowExStyle)
	}
	return r.Right - r.Left, r.Bottom - r.Top
}

func (c *win) setIcons() {
	for i, size := range []int{16, 32} {
		px := int(mulDiv(int32(size), c.dpi))
		var buf bytes.Buffer
		if err := png.Encode(&buf, icon.Render(px, icon.LightBusy)); err != nil {
			continue
		}
		data := buf.Bytes()
		h, _, _ := procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, uintptr(px), uintptr(px), 0)
		c.icons[i] = h
		which := uintptr(iconSmall)
		if size == 32 {
			which = iconBig
		}
		sendMessage(c.hwnd, wmSetIcon, which, h)
	}
}

func (c *win) fill(f Form) {
	setText(c.host, f.Host)
	setText(c.port, f.Port)
	setText(c.pcName, f.PCName)
	setText(c.ignore, strings.ReplaceAll(f.Ignore, "\n", "\r\n"))
	setText(c.names, strings.ReplaceAll(f.Names, "\n", "\r\n"))
	selectChoice(c.lang, c.langChoices, f.Language)
	selectChoice(c.displayLang, c.displayChoices, f.DisplayLanguage)
	selectChoice(c.flip, c.flipChoices, f.DisplayFlip)
	setCheck(c.sendApps, f.SendApps)
	setCheck(c.doorbell, f.Doorbell)
	setCheck(c.notifications, f.Notifications)
	setCheck(c.autostart, f.Autostart)
}

func (c *win) form() Form {
	return Form{
		Host: getText(c.host), Port: getText(c.port), Key: getText(c.key), RemoveKey: isChecked(c.removeKey),
		PCName:          getText(c.pcName),
		Language:        chosen(c.lang, c.langChoices),
		DisplayLanguage: chosen(c.displayLang, c.displayChoices),
		DisplayFlip:     chosen(c.flip, c.flipChoices),
		SendApps:        isChecked(c.sendApps), Doorbell: isChecked(c.doorbell),
		Notifications: isChecked(c.notifications), Autostart: isChecked(c.autostart),
		Ignore: strings.ReplaceAll(getText(c.ignore), "\r\n", "\n"),
		Names:  strings.ReplaceAll(getText(c.names), "\r\n", "\n"),
	}
}

func selectChoice(h windows.HWND, choices []Choice, value string) {
	for i, ch := range choices {
		if ch.Value == value {
			sendMessage(h, cbSetCurSel, uintptr(i), 0)
			return
		}
	}
	sendMessage(h, cbSetCurSel, 0, 0)
}

func chosen(h windows.HWND, choices []Choice) string {
	i := int(int32(sendMessage(h, cbGetCurSel, 0, 0)))
	if i < 0 || i >= len(choices) {
		return ""
	}
	return choices[i].Value
}

func setCheck(h windows.HWND, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	sendMessage(h, bmSetCheck, v, 0)
}

func isChecked(h windows.HWND) bool { return sendMessage(h, bmGetCheck, 0, 0) == 1 }

func (c *win) save() {
	err := c.model.Save(c.form())
	if err == nil {
		procDestroyWindow.Call(uintptr(c.hwnd))
		return
	}
	c.message(c.model.SaveError(err))
	var fe *FieldError
	if errors.As(err, &fe) {
		field := map[Field]windows.HWND{FieldHost: c.host, FieldPort: c.port, FieldKey: c.key, FieldNames: c.names}[fe.Field]
		if field != 0 {
			procSetFocus.Call(uintptr(field))
			sendMessage(field, emSetSel, 0, ^uintptr(0))
		}
	}
}

func (c *win) message(text string) {
	procMessageBoxW.Call(uintptr(c.hwnd), uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(c.model.T("settings.title")))), mbOK|mbIconWarning)
}

// startTest runs the connection test on another goroutine; the window stays
// responsive and shows the outcome when wmTestDone arrives.
func (c *win) startTest() {
	f := c.form()
	enableWindow(c.testBtn, false)
	setText(c.result, c.model.T("settings.testing"))
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelTest = cancel
	hwnd := c.hwnd
	go func() {
		_, msg := c.model.Test(ctx, f)
		c.testMu.Lock()
		c.testMsg = msg
		c.testMu.Unlock()
		postMessage(hwnd, wmTestDone, 0, 0)
	}()
}

func (c *win) testDone() {
	c.testMu.Lock()
	msg := c.testMsg
	c.testMu.Unlock()
	setText(c.result, msg)
	enableWindow(c.testBtn, true)
}

func (c *win) dpiChanged(dpi uint32) {
	c.dpi = dpi
	old := c.font
	c.font = messageFont(dpi)
	for _, ct := range c.ctrls {
		sendMessage(ct.hwnd, wmSetFont, c.font, 0)
	}
	if old != 0 {
		procDeleteObject.Call(old)
	}
	c.layout()
	// Keep the window where it is (the user is dragging it to another
	// monitor) and resize it for the new DPI.
	var cur rect
	procGetWindowRect.Call(uintptr(c.hwnd), uintptr(unsafe.Pointer(&cur)))
	w, h := c.windowSize()
	procSetWindowPos.Call(uintptr(c.hwnd), 0, uintptr(cur.Left), uintptr(cur.Top), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
}

func (c *win) destroyed() {
	if c.cancelTest != nil {
		c.cancelTest()
	}
	if c.font != 0 {
		procDeleteObject.Call(c.font)
	}
	for _, h := range c.icons {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
	windowsMu.Lock()
	delete(byHwnd, c.hwnd)
	windowsMu.Unlock()
	procPostQuitMessage.Call(0)
}

func wndProc(hwnd windows.HWND, m, wParam, lParam uintptr) uintptr {
	windowsMu.Lock()
	c := byHwnd[hwnd]
	windowsMu.Unlock()
	if c != nil {
		switch m {
		case wmCommand:
			switch id, code := wParam&0xffff, (wParam>>16)&0xffff; {
			case id == idSave:
				c.save()
				return 0
			case id == idCancel:
				procDestroyWindow.Call(uintptr(hwnd))
				return 0
			case id == idTest && code == bnClicked:
				c.startTest()
				return 0
			}
		case wmTestDone:
			c.testDone()
			return 0
		case wmDpiChanged:
			c.dpiChanged(uint32(wParam & 0xffff))
			return 0
		case wmClose:
			procDestroyWindow.Call(uintptr(hwnd))
			return 0
		case wmDestroy:
			c.destroyed()
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), m, wParam, lParam)
	return r
}

// windowDPI is the DPI of the window's monitor (Windows 10 1607+), else the
// system DPI.
func windowDPI(hwnd windows.HWND) uint32 {
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(uintptr(hwnd)); d != 0 {
			return uint32(d)
		}
	}
	dc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, dc)
	if d, _, _ := procGetDeviceCaps.Call(dc, logPixelsY); d != 0 {
		return uint32(d)
	}
	return 96
}

// messageFont creates the system dialog font (Segoe UI 9 pt on current
// Windows) for dpi.
func messageFont(dpi uint32) uintptr {
	ncm := nonClientMetrics{}
	ncm.Size = uint32(unsafe.Sizeof(ncm))
	if procSystemParametersInfoForDp.Find() == nil {
		procSystemParametersInfoForDp.Call(spiGetNonClientMetrics, uintptr(ncm.Size), uintptr(unsafe.Pointer(&ncm)), 0, uintptr(dpi))
	} else {
		procSystemParametersInfoW.Call(spiGetNonClientMetrics, uintptr(ncm.Size), uintptr(unsafe.Pointer(&ncm)), 0)
	}
	lf := ncm.MessageFont
	if lf.Height == 0 {
		lf.Height = -mulDiv(12, dpi) // 9 pt
		copy(lf.FaceName[:], windows.StringToUTF16("Segoe UI"))
	}
	f, _, _ := procCreateFontIndirect.Call(uintptr(unsafe.Pointer(&lf)))
	return f
}

// capture renders the window into an image with PrintWindow.
func capture(hwnd windows.HWND) (*image.NRGBA, error) {
	var r rect
	procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w <= 0 || h <= 0 {
		return nil, errors.New("settingsui: empty window")
	}
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDC.Call(mem)
	bi := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, err := procCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		return nil, fmt.Errorf("settingsui: CreateDIBSection: %w", err)
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)
	if ok, _, err := procPrintWindow.Call(uintptr(hwnd), mem, pwRenderFullContent); ok == 0 {
		return nil, fmt.Errorf("settingsui: PrintWindow: %w", err)
	}
	src := unsafe.Slice((*byte)(bits), w*h*4)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4+0] = src[i*4+2]
		img.Pix[i*4+1] = src[i*4+1]
		img.Pix[i*4+2] = src[i*4+0]
		img.Pix[i*4+3] = 0xff
	}
	return img, nil
}
