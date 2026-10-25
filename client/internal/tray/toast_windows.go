//go:build windows

package tray

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Cecchellone/KnockBlock/client/internal/icon"
)

// The doorbell notification is a Windows toast with a reply box, shown
// through the WinRT notification API (Windows.UI.Notifications) called by
// vtable with no CGO, like the detect package's COM code. Interfaces and
// vtable order come from the Windows SDK headers
// windows.ui.notifications.h, windows.data.xml.dom.h and
// windows.foundation.h (10.0.26100.0); IInspectable's six methods come
// first. Other notifications stay tray balloons (notify_windows.go).
//
// An app without a package needs an AppUserModelID registered under
// HKCU\Software\Classes\AppUserModelId (name and icon) to show toasts. The
// reply arrives through the toast's Activated event, raised in this
// process while it runs: no COM server or Start menu shortcut is needed.

const toastAppID = "Cecchellone.KnockBlock"

var (
	combase                       = windows.NewLazySystemDLL("combase.dll")
	procRoGetActivationFactory    = combase.NewProc("RoGetActivationFactory")
	procRoActivateInstance        = combase.NewProc("RoActivateInstance")
	procWindowsCreateString       = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = combase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuffer = combase.NewProc("WindowsGetStringRawBuffer")

	iidIUnknown                      = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIAgileObject                  = windows.GUID{Data1: 0x94EA2B94, Data2: 0xE9CC, Data3: 0x49E0, Data4: [8]byte{0xC0, 0xFF, 0xEE, 0x64, 0xCA, 0x8F, 0x5B, 0x90}}
	iidIToastNotificationManagerStat = windows.GUID{Data1: 0x50AC103F, Data2: 0xD235, Data3: 0x4598, Data4: [8]byte{0xBB, 0xEF, 0x98, 0xFE, 0x4D, 0x1A, 0x3A, 0xD4}}
	iidIToastNotificationFactory     = windows.GUID{Data1: 0x04124B20, Data2: 0x82C6, Data3: 0x4229, Data4: [8]byte{0xB1, 0x09, 0xFD, 0x9E, 0xD4, 0x66, 0x2B, 0x53}}
	iidIXmlDocument                  = windows.GUID{Data1: 0xF7F3A506, Data2: 0x1E87, Data3: 0x42D6, Data4: [8]byte{0xBC, 0xFB, 0xB8, 0xC8, 0x09, 0xFA, 0x54, 0x94}}
	iidIXmlDocumentIO                = windows.GUID{Data1: 0x6CD0E74E, Data2: 0xEE65, Data3: 0x4489, Data4: [8]byte{0x9E, 0xBF, 0xCA, 0x43, 0xE8, 0x7B, 0xA6, 0x37}}
	iidToastActivatedHandler         = windows.GUID{Data1: 0xAB54DE2D, Data2: 0x97D9, Data3: 0x5528, Data4: [8]byte{0xB6, 0xAD, 0x10, 0x5A, 0xFE, 0x15, 0x65, 0x30}} // TypedEventHandler<ToastNotification, Object>
	iidIToastActivatedEventArgs      = windows.GUID{Data1: 0xE3BF92F3, Data2: 0xC197, Data3: 0x436F, Data4: [8]byte{0x82, 0x65, 0x06, 0x25, 0x82, 0x4F, 0x8D, 0xAC}}
	iidIToastActivatedEventArgs2     = windows.GUID{Data1: 0xAB7DA512, Data2: 0xCC61, Data3: 0x568E, Data4: [8]byte{0x81, 0xBE, 0x30, 0x4A, 0xC3, 0x10, 0x38, 0xFA}}
	iidIMapStringInspectable         = windows.GUID{Data1: 0x1B0D3570, Data2: 0x0877, Data3: 0x5EC2, Data4: [8]byte{0x8A, 0x2C, 0x3B, 0x95, 0x39, 0x50, 0x6A, 0xCA}}
	iidIPropertyValue                = windows.GUID{Data1: 0x4BD682DD, Data2: 0x7554, Data3: 0x40E9, Data4: [8]byte{0x9A, 0x9B, 0x82, 0x65, 0x4E, 0xDE, 0x7E, 0x62}}
)

// Vtable indices.
const (
	vQueryInterface            = 0
	vRelease                   = 2
	vCreateToastNotifierWithId = 7  // IToastNotificationManagerStatics
	vNotifierShow              = 6  // IToastNotifier
	vNotifierGetSetting        = 8  //
	vCreateToastNotification   = 6  // IToastNotificationFactory
	vToastAddActivated         = 11 // IToastNotification
	vLoadXml                   = 6  // IXmlDocumentIO
	vActivatedGetArguments     = 6  // IToastActivatedEventArgs
	vActivatedGetUserInput     = 6  // IToastActivatedEventArgs2
	vMapLookup                 = 6  // IMap<String, Object>
	vPropertyValueGetString    = 19 // IPropertyValue
	notificationSettingEnabled = 0  // NotificationSetting
	sFalse                     = 1
	eNoInterface               = 0x80004002
	replyInput, replyAction    = "reply", "reply"
	maxReply                   = 120 // bytes; the light cuts there too
)

// comObj is a COM interface pointer: a pointer to a vtable pointer.
type comObj struct{ vtbl *[32]uintptr }

func (o *comObj) call(method int, args ...uintptr) uint32 {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return uint32(r)
}

func (o *comObj) query(iid *windows.GUID) *comObj {
	var out *comObj
	if failedHR(o.call(vQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) {
		return nil
	}
	return out
}

func releaseObj(p **comObj) {
	if *p != nil {
		(*p).call(vRelease)
		*p = nil
	}
}

func failedHR(hr uint32) bool { return int32(hr) < 0 }

// hstring is a WinRT string handle.
type hstring uintptr

func newHString(s string) (hstring, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h hstring
	if hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h))); failedHR(uint32(hr)) {
		return 0, fmt.Errorf("tray: WindowsCreateString: HRESULT %#x", uint32(hr))
	}
	return h, nil
}

func (h hstring) free() {
	if h != 0 {
		procWindowsDeleteString.Call(uintptr(h))
	}
}

func (h hstring) String() string {
	var n uint32
	r, _, _ := procWindowsGetStringRawBuffer.Call(uintptr(h), uintptr(unsafe.Pointer(&n)))
	if r == 0 || n == 0 {
		return ""
	}
	// Reinterpret the returned address without a uintptr conversion that
	// vet's unsafeptr check would flag; the buffer lives as long as h.
	p := *(**uint16)(unsafe.Pointer(&r))
	return windows.UTF16ToString(unsafe.Slice(p, n))
}

// activationFactory returns a WinRT class's factory or statics interface.
func activationFactory(class string, iid *windows.GUID) (*comObj, error) {
	h, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer h.free()
	var f *comObj
	if hr, _, _ := procRoGetActivationFactory.Call(uintptr(h), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&f))); failedHR(uint32(hr)) {
		return nil, fmt.Errorf("tray: activation factory of %s: HRESULT %#x", class, uint32(hr))
	}
	return f, nil
}

// errToastsOff means Windows does not show this app's toasts (turned off in
// Settings > Notifications, or by policy).
var errToastsOff = errors.New("tray: notifications are turned off for KnockBlock")

// toastRequest is a reply toast to show on the toast thread.
type toastRequest struct {
	xml   string
	reply func(string)
	done  chan error
}

// The toast thread stays in the multithreaded apartment for the life of
// the process, so the toast it showed (and its Activated handler) outlive
// the call that showed it.
var (
	toastOnce sync.Once
	toastReqs chan toastRequest

	// Read by the Activated handler (another thread).
	toastMu    sync.Mutex
	toastReply func(string)
)

// Our TypedEventHandler<ToastNotification, Object>: a COM object whose
// first field is its vtable. It lives for the whole process.
type toastHandler struct{ vtbl *[4]uintptr }

var (
	handlerVtbl [4]uintptr
	handler     toastHandler
)

func startToastThread() {
	handlerVtbl[0] = syscall.NewCallback(func(this *toastHandler, riid *windows.GUID, out *unsafe.Pointer) uintptr {
		if *riid == iidIUnknown || *riid == iidIAgileObject || *riid == iidToastActivatedHandler {
			*out = unsafe.Pointer(this)
			return 0
		}
		*out = nil
		return eNoInterface
	})
	handlerVtbl[1] = syscall.NewCallback(func(this *toastHandler) uintptr { return 1 })
	handlerVtbl[2] = syscall.NewCallback(func(this *toastHandler) uintptr { return 1 })
	handlerVtbl[3] = syscall.NewCallback(func(this *toastHandler, sender, args *comObj) uintptr {
		if text, ok := replyText(args); ok {
			toastMu.Lock()
			fn := toastReply
			toastMu.Unlock()
			if fn != nil {
				go fn(text)
			}
		}
		return 0
	})
	handler.vtbl = &handlerVtbl
	toastReqs = make(chan toastRequest)
	go toastThread()
}

func toastThread() {
	runtime.LockOSThread() // never unlocked: this thread stays in the MTA
	initErr := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED)
	if errors.Is(initErr, syscall.Errno(sFalse)) {
		initErr = nil
	}
	var notifier, toast *comObj // the last toast stays referenced
	for req := range toastReqs {
		if initErr != nil {
			req.done <- fmt.Errorf("tray: CoInitializeEx: %w", initErr)
			continue
		}
		if notifier == nil {
			n, err := newNotifier()
			if err != nil {
				req.done <- err
				continue
			}
			notifier = n
		}
		var setting uint32
		if !failedHR(notifier.call(vNotifierGetSetting, uintptr(unsafe.Pointer(&setting)))) && setting != notificationSettingEnabled {
			req.done <- errToastsOff
			continue
		}
		t, err := newToast(req.xml)
		if err != nil {
			req.done <- err
			continue
		}
		toastMu.Lock()
		toastReply = req.reply
		toastMu.Unlock()
		if hr := notifier.call(vNotifierShow, uintptr(unsafe.Pointer(t))); failedHR(hr) {
			releaseObj(&t)
			req.done <- fmt.Errorf("tray: showing the toast: HRESULT %#x", hr)
			continue
		}
		releaseObj(&toast)
		toast = t
		req.done <- nil
	}
}

func newNotifier() (*comObj, error) {
	if err := registerToastApp(); err != nil {
		return nil, err
	}
	statics, err := activationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidIToastNotificationManagerStat)
	if err != nil {
		return nil, err
	}
	defer releaseObj(&statics)
	id, err := newHString(toastAppID)
	if err != nil {
		return nil, err
	}
	defer id.free()
	var n *comObj
	if hr := statics.call(vCreateToastNotifierWithId, uintptr(id), uintptr(unsafe.Pointer(&n))); failedHR(hr) {
		return nil, fmt.Errorf("tray: creating the toast notifier: HRESULT %#x", hr)
	}
	return n, nil
}

// newToast builds a toast from its XML, with our Activated handler.
func newToast(content string) (*comObj, error) {
	class, err := newHString("Windows.Data.Xml.Dom.XmlDocument")
	if err != nil {
		return nil, err
	}
	defer class.free()
	var inst *comObj
	if hr, _, _ := procRoActivateInstance.Call(uintptr(class), uintptr(unsafe.Pointer(&inst))); failedHR(uint32(hr)) {
		return nil, fmt.Errorf("tray: creating an XML document: HRESULT %#x", uint32(hr))
	}
	defer releaseObj(&inst)
	io := inst.query(&iidIXmlDocumentIO)
	if io == nil {
		return nil, errors.New("tray: XML document without IXmlDocumentIO")
	}
	defer releaseObj(&io)
	text, err := newHString(content)
	if err != nil {
		return nil, err
	}
	defer text.free()
	if hr := io.call(vLoadXml, uintptr(text)); failedHR(hr) {
		return nil, fmt.Errorf("tray: toast XML: HRESULT %#x", hr)
	}
	doc := inst.query(&iidIXmlDocument)
	if doc == nil {
		return nil, errors.New("tray: XML document without IXmlDocument")
	}
	defer releaseObj(&doc)

	factory, err := activationFactory("Windows.UI.Notifications.ToastNotification", &iidIToastNotificationFactory)
	if err != nil {
		return nil, err
	}
	defer releaseObj(&factory)
	var toast *comObj
	if hr := factory.call(vCreateToastNotification, uintptr(unsafe.Pointer(doc)), uintptr(unsafe.Pointer(&toast))); failedHR(hr) {
		return nil, fmt.Errorf("tray: creating the toast: HRESULT %#x", hr)
	}
	var token int64
	if hr := toast.call(vToastAddActivated, uintptr(unsafe.Pointer(&handler)), uintptr(unsafe.Pointer(&token))); failedHR(hr) {
		releaseObj(&toast)
		return nil, fmt.Errorf("tray: toast Activated event: HRESULT %#x", hr)
	}
	return toast, nil
}

// replyText returns the text typed in the reply box when the toast was
// activated by sending it (Enter or the send button), not by a click.
func replyText(args *comObj) (string, bool) {
	if args == nil {
		return "", false
	}
	a1 := args.query(&iidIToastActivatedEventArgs)
	if a1 == nil {
		return "", false
	}
	defer releaseObj(&a1)
	var arg hstring
	if failedHR(a1.call(vActivatedGetArguments, uintptr(unsafe.Pointer(&arg)))) {
		return "", false
	}
	defer arg.free()
	if arg.String() != replyAction {
		return "", false
	}
	a2 := args.query(&iidIToastActivatedEventArgs2)
	if a2 == nil {
		return "", false
	}
	defer releaseObj(&a2)
	var set *comObj
	if failedHR(a2.call(vActivatedGetUserInput, uintptr(unsafe.Pointer(&set)))) || set == nil {
		return "", false
	}
	defer releaseObj(&set)
	m := set.query(&iidIMapStringInspectable)
	if m == nil {
		return "", false
	}
	defer releaseObj(&m)
	key, err := newHString(replyInput)
	if err != nil {
		return "", false
	}
	defer key.free()
	var v *comObj
	if failedHR(m.call(vMapLookup, uintptr(key), uintptr(unsafe.Pointer(&v)))) || v == nil {
		return "", false
	}
	defer releaseObj(&v)
	pv := v.query(&iidIPropertyValue)
	if pv == nil {
		return "", false
	}
	defer releaseObj(&pv)
	var s hstring
	if failedHR(pv.call(vPropertyValueGetString, uintptr(unsafe.Pointer(&s)))) {
		return "", false
	}
	defer s.free()
	text := s.String()
	return text, text != ""
}

// registerToastApp registers KnockBlock's AppUserModelID for toasts: the
// name and icon Windows shows on them. The icon is drawn once into the
// user's cache folder.
func registerToastApp() error {
	dir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	iconPath := filepath.Join(dir, "KnockBlock", "toast.png")
	if _, err := os.Stat(iconPath); err != nil {
		var buf bytes.Buffer
		if err := png.Encode(&buf, icon.Render(96, icon.LightBusy)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(iconPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(iconPath, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+toastAppID, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("tray: registering for notifications: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue("DisplayName", "KnockBlock"); err != nil {
		return err
	}
	return k.SetStringValue("IconUri", iconPath)
}

// replyToast describes the doorbell toast.
type replyToast struct {
	Title, Body       string
	Placeholder, Send string // the reply box's hint and send button
	Sound             bool   // Windows' notification sound
}

// toastXML builds the toast: title, text, and a reply box that sends with
// Enter or its button.
func (t replyToast) toastXML() string {
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s)) // writes to memory: cannot fail
		return b.String()
	}
	audio := `<audio silent="true"/>`
	if t.Sound {
		audio = ""
	}
	return `<toast launch="doorbell"><visual><binding template="ToastGeneric">` +
		`<text>` + esc(t.Title) + `</text><text>` + esc(t.Body) + `</text>` +
		`</binding></visual><actions>` +
		`<input id="` + replyInput + `" type="text" placeHolderContent="` + esc(t.Placeholder) + `"/>` +
		`<action content="` + esc(t.Send) + `" arguments="` + replyAction + `" hint-inputId="` + replyInput + `" activationType="foreground"/>` +
		`</actions>` + audio + `</toast>`
}

// showReplyToast shows t; reply is called, on its own goroutine, with the
// text the user sends from it.
func showReplyToast(t replyToast, reply func(string)) error {
	toastOnce.Do(startToastThread)
	req := toastRequest{xml: t.toastXML(), reply: reply, done: make(chan error, 1)}
	toastReqs <- req
	return <-req.done
}
