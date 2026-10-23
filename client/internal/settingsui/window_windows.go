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
	"strconv"
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

// Tabs is the number of tabs.
const Tabs = 4

// SelectTab shows tab i (0-based) of the open window.
func (w *Window) SelectTab(i int) {
	w.mu.Lock()
	c := w.cur
	w.mu.Unlock()
	if c != nil {
		sendMessage(c.hwnd, wmSelectTab, uintptr(i), 0)
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
	idTabs = 101
)

// Messages to the window thread: the connection test finished; the light's
// settings were read (or could not be).
const (
	wmTestDone    = wmApp + 1
	wmDeviceReady = wmApp + 2
	wmSelectTab   = wmApp + 3
)

// box is a control's place in 96-DPI client coordinates.
type box struct{ X, Y, W, H int32 }

type control struct {
	hwnd windows.HWND
	box  box
	page int  // tab showing it; -1 on every tab
	off  bool // never shown (a setting the light does not have)
}

// Tabs, in order.
const (
	pageConnection = iota
	pageLight
	pagePC
	pageApps
)

// win is one open window. Every field but the ones under mu belongs to the
// window thread.
type win struct {
	model *Model
	ready chan error
	done  chan struct{}

	hwnd     windows.HWND
	inst     windows.Handle
	dpi      uint32
	font     uintptr
	icons    [2]uintptr
	ctrls    []control
	building int // page of the controls being created
	page     int // page shown

	tabs                                        windows.HWND
	host, port, key, removeKey, testBtn, result windows.HWND
	devStatus, devLang, devFlip, devBacklight   windows.HWND
	devLED, devPrivacy                          windows.HWND
	pcName, lang                                windows.HWND
	sendApps, doorbell, notify, autostart       windows.HWND
	ignore, names                               windows.HWND
	langChoices, devLangChoices, flipChoices    []Choice
	backlightChoices, ledChoices, notifyChoices []Choice
	cancelTest, cancelDevice                    context.CancelFunc
	device                                      *Device // nil until read, or if unreachable

	mu        sync.Mutex
	testMsg   string
	devResult *Device
	devErr    error
	closed    bool
}

// Client area size, in 96-DPI units.
const clientW, clientH = 520, 432

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
		// Ctrl+Tab and Ctrl+Shift+Tab switch tabs.
		if m.Message == wmKeyDown && m.WParam == vkTab && keyDown(vkControl) {
			step := 1
			if keyDown(vkShift) {
				step = 3
			}
			c.showPage((c.page + step) % Tabs)
			continue
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
			Background: windows.Handle(windowBrush()),
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
	c.openDevice()
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
	label := func(text string, b box) windows.HWND { return c.add("STATIC", text, ssNoPrefix, 0, 0, b) }
	edit := func(style uint32, b box) windows.HWND {
		return c.add("EDIT", "", wsTabStop|esAutoHScroll|style, wsExClientEdge, 0, b)
	}
	combo := func(choices []Choice, b box) windows.HWND {
		b.H = 240 // height of the open list
		h := c.add("COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDownList, 0, 0, b)
		fillCombo(h, choices)
		return h
	}
	check := func(text string, b box) windows.HWND {
		return c.add("BUTTON", text, wsTabStop|bsAutoCheckBox|bsMultiline, 0, 0, b)
	}
	const x, iw, cw = 28, 464, 260 // page left edge, full and combo widths

	c.building = -1
	// Only the strip of tab headers: the pages lie on the window background,
	// so labels and check boxes match it (the themed tab body is a shade
	// lighter and would show their rectangles).
	c.tabs = c.add("SysTabControl32", "", wsTabStop|wsClipSiblings, 0, idTabs, box{12, 12, clientW - 24, 26})
	for i, key := range []string{"settings.tab_connection", "settings.device", "settings.this_pc", "settings.apps"} {
		item := tcItem{Mask: tcifText, Text: utf16Ptr(t(key))}
		sendMessage(c.tabs, tcmInsertItem, uintptr(i), uintptr(unsafe.Pointer(&item)))
	}

	// Connection.
	c.building = pageConnection
	label(t("settings.host"), box{x, 48, 300, 16})
	label(t("settings.port"), box{x + 316, 48, 80, 16})
	c.host = edit(0, box{x, 66, 300, 23})
	sendMessage(c.host, emSetCueBanner, 0, uintptr(unsafe.Pointer(utf16Ptr("knockblock.local"))))
	c.port = edit(esNumber, box{x + 316, 66, 80, 23})
	sendMessage(c.port, emSetLimitText, 5, 0)
	label(t("settings.key"), box{x, 99, iw, 16})
	c.key = edit(esPassword, box{x, 117, iw, 23})
	if c.model.KeySaved() {
		sendMessage(c.key, emSetCueBanner, 0, uintptr(unsafe.Pointer(utf16Ptr(t("settings.key_saved")))))
	}
	c.removeKey = check(t("settings.key_remove"), box{x, 150, iw, 20})
	enableWindow(c.removeKey, c.model.KeySaved())
	c.testBtn = c.add("BUTTON", t("settings.test"), wsTabStop|bsPushButton, 0, idTest, box{x, 180, 160, 26})
	c.result = label("", box{x, 214, iw, 48})

	// Light: read from the device when it answers (deviceReady).
	c.building = pageLight
	c.devStatus = label(t("settings.device_connecting"), box{x, 48, iw, 36})
	label(t("settings.display_language"), box{x, 92, iw, 16})
	c.devLang = combo(nil, box{x, 110, cw, 23})
	label(t("settings.display_flip"), box{x, 143, iw, 16})
	c.flipChoices = c.model.Orientations()
	c.devFlip = combo(c.flipChoices, box{x, 161, cw, 23})
	label(t("settings.display_brightness"), box{x, 194, iw, 16})
	c.backlightChoices = c.model.Percentages(true)
	c.devBacklight = combo(c.backlightChoices, box{x, 212, cw, 23})
	label(t("settings.led_brightness"), box{x, 245, iw, 16})
	c.ledChoices = c.model.Percentages(false)
	c.devLED = combo(c.ledChoices, box{x, 263, cw, 23})
	c.devPrivacy = check(t("settings.privacy"), box{x, 298, iw, 30})
	c.enableDevice(false)

	// This PC.
	c.building = pagePC
	label(t("settings.pc_name"), box{x, 48, iw, 16})
	c.pcName = edit(0, box{x, 66, iw, 23})
	sendMessage(c.pcName, emSetCueBanner, 0, uintptr(unsafe.Pointer(utf16Ptr(c.model.DefaultPCName()))))
	sendMessage(c.pcName, emSetLimitText, 32, 0)
	label(t("settings.pc_name_help", "default", c.model.DefaultPCName()), box{x, 95, iw, 30})
	label(t("settings.language"), box{x, 131, iw, 16})
	c.langChoices = c.model.Languages("settings.language_auto")
	c.lang = combo(c.langChoices, box{x, 149, cw, 23})
	c.sendApps = check(t("settings.send_apps"), box{x, 186, iw, 30})
	c.doorbell = check(t("settings.doorbell"), box{x, 220, iw, 30})
	label(t("settings.notify"), box{x, 256, iw, 16})
	c.notifyChoices = c.model.NotifyModes()
	c.notify = combo(c.notifyChoices, box{x, 274, cw, 23})
	c.autostart = check(t("settings.autostart"), box{x, 306, iw, 30})

	// Apps.
	c.building = pageApps
	multi := uint32(wsTabStop | wsVScroll | esMultiline | esAutoVScroll | esWantReturn)
	label(t("settings.ignore"), box{x, 48, iw, 16})
	c.ignore = c.add("EDIT", "", multi, wsExClientEdge, 0, box{x, 66, iw, 76})
	label(t("settings.ignore_help"), box{x, 146, iw, 32})
	label(t("settings.names"), box{x, 186, iw, 16})
	c.names = c.add("EDIT", "", multi, wsExClientEdge, 0, box{x, 204, iw, 76})
	label(t("settings.names_help"), box{x, 284, iw, 32})

	// Buttons.
	c.building = -1
	c.add("BUTTON", t("settings.save"), wsTabStop|bsDefPushButton, 0, idSave, box{clientW - 12 - 208, 394, 100, 26})
	c.add("BUTTON", t("settings.cancel"), wsTabStop|bsPushButton, 0, idCancel, box{clientW - 12 - 100, 394, 100, 26})
	c.showPage(pageConnection)
}

func fillCombo(h windows.HWND, choices []Choice) {
	for _, ch := range choices {
		sendMessage(h, cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(ch.Label))))
	}
}

// showPage shows the controls of one tab and hides the others.
func (c *win) showPage(p int) {
	c.page = p
	sendMessage(c.tabs, tcmSetCurSel, uintptr(p), 0)
	for _, ct := range c.ctrls {
		if ct.page < 0 {
			continue
		}
		show := uintptr(swHide)
		if ct.page == p && !ct.off {
			show = swShow
		}
		procShowWindow.Call(uintptr(ct.hwnd), show)
	}
}

// enableDevice enables the Light tab's settings once they are read.
func (c *win) enableDevice(on bool) {
	for _, h := range []windows.HWND{c.devLang, c.devFlip, c.devBacklight, c.devLED, c.devPrivacy} {
		enableWindow(h, on)
	}
}

// hide removes a control for good (with its label, the control before it).
func (c *win) hide(h windows.HWND, withLabel bool) {
	for i := range c.ctrls {
		if c.ctrls[i].hwnd == h {
			c.ctrls[i].off = true
			if withLabel && i > 0 {
				c.ctrls[i-1].off = true
			}
		}
	}
}

// openDevice reads the light's settings on another goroutine.
func (c *win) openDevice() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelDevice = cancel
	hwnd := c.hwnd
	go func() {
		d, err := c.model.OpenDevice(ctx)
		c.mu.Lock()
		if c.closed { // the window is gone: nobody will take it
			c.mu.Unlock()
			if d != nil {
				d.Close()
			}
			return
		}
		c.devResult, c.devErr = d, err
		c.mu.Unlock()
		postMessage(hwnd, wmDeviceReady, 0, 0)
	}()
}

// deviceReady shows the light's settings, or why they are missing.
func (c *win) deviceReady() {
	c.mu.Lock()
	d, err := c.devResult, c.devErr
	c.devResult = nil
	c.mu.Unlock()
	if err != nil {
		setText(c.devStatus, c.model.T("settings.device_unreachable"))
		return
	}
	c.device = d
	s := d.Settings()
	setText(c.devStatus, c.model.T("settings.device_ready", "device", d.Name))
	c.devLangChoices = c.model.DisplayLanguages(s.Languages)
	fillCombo(c.devLang, c.devLangChoices)
	selectChoice(c.devLang, c.devLangChoices, s.Language)
	flip := "off"
	if s.Flip {
		flip = "on"
	}
	selectChoice(c.devFlip, c.flipChoices, flip)
	selectChoice(c.devBacklight, c.backlightChoices, strconv.Itoa(NearestPercent(s.Backlight, true)))
	selectChoice(c.devLED, c.ledChoices, strconv.Itoa(NearestPercent(s.LED, false)))
	setCheck(c.devPrivacy, s.Privacy)
	c.enableDevice(true)
	if len(s.Languages) == 0 {
		c.hide(c.devLang, true)
	}
	if !s.HasFlip {
		c.hide(c.devFlip, true)
	}
	if !s.HasBacklight {
		c.hide(c.devBacklight, true)
	}
	if !s.HasLED {
		c.hide(c.devLED, true)
	}
	if !s.HasPrivacy {
		c.hide(c.devPrivacy, false)
	}
	c.showPage(c.page)
}

// deviceForm is the Light tab as DeviceSettings.
func (c *win) deviceForm() DeviceSettings {
	s := c.device.Settings()
	if v := chosen(c.devLang, c.devLangChoices); v != "" {
		s.Language = v
	}
	s.Flip = chosen(c.devFlip, c.flipChoices) == "on"
	s.Privacy = isChecked(c.devPrivacy)
	if v, err := strconv.Atoi(chosen(c.devBacklight, c.backlightChoices)); err == nil && v != NearestPercent(s.Backlight, true) {
		s.Backlight = v // untouched entries keep the exact value read
	}
	if v, err := strconv.Atoi(chosen(c.devLED, c.ledChoices)); err == nil && v != NearestPercent(s.LED, false) {
		s.LED = v
	}
	return s
}

// add creates a child control; layout places it.
func (c *win) add(class, text string, style, exStyle uint32, id int, b box) windows.HWND {
	h, _, _ := procCreateWindowExW.Call(uintptr(exStyle),
		uintptr(unsafe.Pointer(utf16Ptr(class))), uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(style|wsChild|wsVisible), 0, 0, 0, 0, uintptr(c.hwnd), uintptr(id), uintptr(c.inst), 0)
	hw := windows.HWND(h)
	sendMessage(hw, wmSetFont, c.font, 0)
	c.ctrls = append(c.ctrls, control{hwnd: hw, box: b, page: c.building})
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
	setCheck(c.sendApps, f.SendApps)
	setCheck(c.doorbell, f.Doorbell)
	selectChoice(c.notify, c.notifyChoices, f.Notify)
	setCheck(c.autostart, f.Autostart)
}

func (c *win) form() Form {
	return Form{
		Host: getText(c.host), Port: getText(c.port), Key: getText(c.key), RemoveKey: isChecked(c.removeKey),
		PCName:   getText(c.pcName),
		Language: chosen(c.lang, c.langChoices),
		SendApps: isChecked(c.sendApps), Doorbell: isChecked(c.doorbell),
		Notify: chosen(c.notify, c.notifyChoices), Autostart: isChecked(c.autostart),
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
	if err != nil {
		c.message(c.model.SaveError(err))
		var fe *FieldError
		if errors.As(err, &fe) {
			field := map[Field]windows.HWND{FieldHost: c.host, FieldPort: c.port, FieldKey: c.key, FieldNames: c.names}[fe.Field]
			page := map[Field]int{FieldHost: pageConnection, FieldPort: pageConnection, FieldKey: pageConnection, FieldNames: pageApps}[fe.Field]
			if field != 0 {
				c.showPage(page)
				procSetFocus.Call(uintptr(field))
				sendMessage(field, emSetSel, 0, ^uintptr(0))
			}
		}
		return
	}
	if c.device != nil {
		if err := c.device.Apply(context.Background(), c.deviceForm()); err != nil {
			c.message(c.model.DeviceError(err))
			c.showPage(pageLight)
			return
		}
	}
	procDestroyWindow.Call(uintptr(c.hwnd))
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
		c.mu.Lock()
		c.testMsg = msg
		c.mu.Unlock()
		postMessage(hwnd, wmTestDone, 0, 0)
	}()
}

func (c *win) testDone() {
	c.mu.Lock()
	msg := c.testMsg
	c.mu.Unlock()
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
	if c.cancelDevice != nil {
		c.cancelDevice()
	}
	if c.device != nil {
		c.device.Close()
	}
	// A light read that arrived but was not shown yet is closed here; one
	// still in progress closes itself.
	c.mu.Lock()
	c.closed = true
	if c.devResult != nil {
		c.devResult.Close()
		c.devResult = nil
	}
	c.mu.Unlock()
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
		case wmCtlColorStatic:
			// Labels, check boxes and group boxes take the window's
			// background instead of the grey dialog colour.
			bg, _, _ := procGetSysColor.Call(colorWindow)
			procSetBkColor.Call(wParam, bg)
			return windowBrush()
		case wmTestDone:
			c.testDone()
			return 0
		case wmDeviceReady:
			c.deviceReady()
			return 0
		case wmSelectTab:
			c.showPage(int(wParam) % Tabs)
			return 0
		case wmNotify:
			// Only the tab control sends notifications here; the selection
			// is read back instead of decoding the NMHDR.
			if sel := int(int32(sendMessage(c.tabs, tcmGetCurSel, 0, 0))); sel >= 0 && sel != c.page {
				c.showPage(sel)
			}
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

func keyDown(vk uintptr) bool {
	r, _, _ := procGetKeyState.Call(vk)
	return int16(r) < 0
}

// windowBrush is the system brush of the window background colour (not to
// be deleted).
func windowBrush() uintptr {
	b, _, _ := procGetSysColorBrush.Call(colorWindow)
	return b
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
