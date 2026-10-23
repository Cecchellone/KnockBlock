//go:build windows

package detect

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wasapi lists the capture (microphone) audio sessions of every active
// recording device through the Core Audio COM interfaces, called by vtable
// index with no COM library (CGO_ENABLED=0). The interfaces are declared in
// the Windows SDK headers mmdeviceapi.h and audiopolicy.h; the vtable
// indices below follow their declaration order, IUnknown's three methods
// first.
//
// Everything is created and released within one call: the session list of a
// session manager is a snapshot, and devices come and go. A poll costs a
// few COM calls into the audio service; the out parameters live in the
// struct, so it allocates nothing.
type wasapi struct {
	enum, devices, dev, mgr, sessions, ctl, ctl2 *comObject
	devCount, state, pid                         uint32
	sessionCount                                 int32
}

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidMMDeviceEnumerator  = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator   = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioSessionManager2 = windows.GUID{Data1: 0x77AA99A0, Data2: 0x1BD6, Data3: 0x484F, Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F}}
	iidIAudioSessionControl2 = windows.GUID{Data1: 0xBFB7FF88, Data2: 0x7239, Data3: 0x4FC9, Data4: [8]byte{0x8F, 0xA2, 0x07, 0xC9, 0x50, 0xBE, 0x9C, 0x6D}}
)

const (
	clsctxAll         = 0x17 // CLSCTX_INPROC_SERVER | INPROC_HANDLER | LOCAL_SERVER | REMOTE_SERVER
	eCapture          = 1    // EDataFlow
	deviceStateActive = 0x1
	sessionActive     = 1 // AudioSessionState: AudioSessionStateActive
	sFalse            = 1 // S_FALSE

	// Vtable indices.
	vRelease                  = 2 // IUnknown
	vQueryInterface           = 0
	vEnumAudioEndpoints       = 3  // IMMDeviceEnumerator
	vCollectionGetCount       = 3  // IMMDeviceCollection
	vCollectionItem           = 4  //
	vDeviceActivate           = 3  // IMMDevice
	vGetSessionEnumerator     = 5  // IAudioSessionManager2 (after IAudioSessionManager's 2)
	vSessionsGetCount         = 3  // IAudioSessionEnumerator
	vSessionsGetSession       = 4  //
	vSessionGetState          = 3  // IAudioSessionControl
	vSessionGetProcessID      = 14 // IAudioSessionControl2 (IAudioSessionControl ends at 11)
	vSessionIsSystemSoundsSes = 15 //
)

// comObject is a COM interface pointer: a pointer to a vtable pointer.
type comObject struct{ vtbl *[32]uintptr }

// call calls a method with up to four arguments after the interface
// pointer. Passing unused trailing arguments is harmless in the x64 calling
// convention (the caller cleans up), so every call passes four.
func (o *comObject) call(method int, a1, a2, a3, a4 uintptr) uint32 {
	r, _, _ := syscall.SyscallN(o.vtbl[method], uintptr(unsafe.Pointer(o)), a1, a2, a3, a4)
	return uint32(r)
}

// release releases *p and clears it.
func release(p **comObject) {
	if *p != nil {
		(*p).call(vRelease, 0, 0, 0, 0)
		*p = nil
	}
}

func failed(hr uint32) bool { return int32(hr) < 0 }

func (w *wasapi) close() error { return nil }

func (w *wasapi) capture(fn func(pid uint32, active bool)) error {
	// COM is set up per thread: keep this goroutine on one thread while it
	// uses COM, and join the multithreaded apartment.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err {
	case nil, syscall.Errno(sFalse): // S_FALSE: already initialized; balance it too
		defer windows.CoUninitialize()
	case syscall.Errno(windows.RPC_E_CHANGED_MODE):
		// This thread is in a single-threaded apartment; COM works there.
	default:
		return fmt.Errorf("detect: CoInitializeEx: %w", err)
	}

	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&w.enum))); failed(uint32(hr)) {
		return fmt.Errorf("detect: creating the audio device enumerator: HRESULT %#x", uint32(hr))
	}
	defer release(&w.enum)
	if hr := w.enum.call(vEnumAudioEndpoints, eCapture, deviceStateActive, uintptr(unsafe.Pointer(&w.devices)), 0); failed(hr) {
		return fmt.Errorf("detect: listing recording devices: HRESULT %#x", hr)
	}
	defer release(&w.devices)
	if hr := w.devices.call(vCollectionGetCount, uintptr(unsafe.Pointer(&w.devCount)), 0, 0, 0); failed(hr) {
		return fmt.Errorf("detect: counting recording devices: HRESULT %#x", hr)
	}
	for i := uint32(0); i < w.devCount; i++ {
		if failed(w.devices.call(vCollectionItem, uintptr(i), uintptr(unsafe.Pointer(&w.dev)), 0, 0)) {
			continue // unplugged meanwhile
		}
		w.deviceSessions(fn)
		release(&w.dev)
	}
	return nil
}

// deviceSessions calls fn for every non-system session of w.dev.
func (w *wasapi) deviceSessions(fn func(pid uint32, active bool)) {
	if failed(w.dev.call(vDeviceActivate, uintptr(unsafe.Pointer(&iidIAudioSessionManager2)), clsctxAll, 0, uintptr(unsafe.Pointer(&w.mgr)))) {
		return
	}
	defer release(&w.mgr)
	if failed(w.mgr.call(vGetSessionEnumerator, uintptr(unsafe.Pointer(&w.sessions)), 0, 0, 0)) {
		return
	}
	defer release(&w.sessions)
	if failed(w.sessions.call(vSessionsGetCount, uintptr(unsafe.Pointer(&w.sessionCount)), 0, 0, 0)) {
		return
	}
	for i := int32(0); i < w.sessionCount; i++ {
		if failed(w.sessions.call(vSessionsGetSession, uintptr(i), uintptr(unsafe.Pointer(&w.ctl)), 0, 0)) {
			continue
		}
		if !failed(w.ctl.call(vQueryInterface, uintptr(unsafe.Pointer(&iidIAudioSessionControl2)), uintptr(unsafe.Pointer(&w.ctl2)), 0, 0)) {
			// IsSystemSoundsSession returns S_OK for the system sounds
			// session, S_FALSE for the others.
			if w.ctl2.call(vSessionIsSystemSoundsSes, 0, 0, 0, 0) == sFalse &&
				!failed(w.ctl2.call(vSessionGetState, uintptr(unsafe.Pointer(&w.state)), 0, 0, 0)) &&
				!failed(w.ctl2.call(vSessionGetProcessID, uintptr(unsafe.Pointer(&w.pid)), 0, 0, 0)) {
				fn(w.pid, w.state == sessionActive)
			}
			release(&w.ctl2)
		}
		release(&w.ctl)
	}
}

// DumpAudioSessions calls fn for every capture audio session, active or
// not, for diagnostics (`knockctl detect`).
func DumpAudioSessions(fn func(pid uint32, exe, path string, active bool)) error {
	procs := &winProcesses{}
	if err := procs.refresh(); err != nil {
		return err
	}
	return (&wasapi{}).capture(func(pid uint32, active bool) {
		exe, path, _ := procs.process(pid)
		fn(pid, exe, path, active)
	})
}
