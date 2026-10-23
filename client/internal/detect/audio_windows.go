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
	enum, devices, dev, mgr, sessions, ctl, ctl2, vol, meter *comObject
	devCount, state, pid, sessMute, devMute                  uint32
	sessionCount                                             int32
	peak                                                     float32
	flow                                                     uintptr // eCapture, or eRender for diagnostics
	withPeak                                                 bool    // read the peak meter (diagnostics only)
}

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidMMDeviceEnumerator   = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator    = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioSessionManager2  = windows.GUID{Data1: 0x77AA99A0, Data2: 0x1BD6, Data3: 0x484F, Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F}}
	iidIAudioSessionControl2  = windows.GUID{Data1: 0xBFB7FF88, Data2: 0x7239, Data3: 0x4FC9, Data4: [8]byte{0x8F, 0xA2, 0x07, 0xC9, 0x50, 0xBE, 0x9C, 0x6D}}
	iidIAudioEndpointVolume   = windows.GUID{Data1: 0x5CDF2C82, Data2: 0x841E, Data3: 0x4546, Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A}}
	iidISimpleAudioVolume     = windows.GUID{Data1: 0x87CE5498, Data2: 0x68D6, Data3: 0x44E5, Data4: [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8}}
	iidIAudioMeterInformation = windows.GUID{Data1: 0xC02216F6, Data2: 0x8C67, Data3: 0x4B5B, Data4: [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64}}
)

const (
	clsctxAll         = 0x17 // CLSCTX_INPROC_SERVER | INPROC_HANDLER | LOCAL_SERVER | REMOTE_SERVER
	eRender           = 0    // EDataFlow
	eCapture          = 1    //
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
	vEndpointGetMute          = 15 // IAudioEndpointVolume
	vSimpleGetMute            = 6  // ISimpleAudioVolume
	vMeterGetPeakValue        = 3  // IAudioMeterInformation
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

func (w *wasapi) capture(fn func(s audioSession)) error {
	w.flow = eCapture
	return w.list(func(s audioSession, _ float32) { fn(s) })
}

// list calls fn with every session of w.flow's active devices.
func (w *wasapi) list(fn func(s audioSession, peak float32)) error {
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
	if hr := w.enum.call(vEnumAudioEndpoints, w.flow, deviceStateActive, uintptr(unsafe.Pointer(&w.devices)), 0); failed(hr) {
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
func (w *wasapi) deviceSessions(fn func(s audioSession, peak float32)) {
	// A device muted in Windows (the sound settings, a mute key) mutes
	// every session on it.
	w.devMute = 0
	if !failed(w.dev.call(vDeviceActivate, uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)), clsctxAll, 0, uintptr(unsafe.Pointer(&w.vol)))) {
		if failed(w.vol.call(vEndpointGetMute, uintptr(unsafe.Pointer(&w.devMute)), 0, 0, 0)) {
			w.devMute = 0
		}
		release(&w.vol)
	}
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
				fn(audioSession{pid: w.pid, active: w.state == sessionActive, muted: w.devMute != 0 || w.sessionMuted()}, w.sessionPeak())
			}
			release(&w.ctl2)
		}
		release(&w.ctl)
	}
}

// sessionMuted reports whether w.ctl's session is muted in Windows (the
// volume mixer, or the app muting its own session).
func (w *wasapi) sessionMuted() bool {
	if failed(w.ctl.call(vQueryInterface, uintptr(unsafe.Pointer(&iidISimpleAudioVolume)), uintptr(unsafe.Pointer(&w.vol)), 0, 0)) {
		return false
	}
	defer release(&w.vol)
	return !failed(w.vol.call(vSimpleGetMute, uintptr(unsafe.Pointer(&w.sessMute)), 0, 0, 0)) && w.sessMute != 0
}

// sessionPeak returns the peak level (0–1) of w.ctl's session, read only
// for diagnostics.
func (w *wasapi) sessionPeak() float32 {
	w.peak = -1
	if !w.withPeak || failed(w.ctl.call(vQueryInterface, uintptr(unsafe.Pointer(&iidIAudioMeterInformation)), uintptr(unsafe.Pointer(&w.meter)), 0, 0)) {
		return w.peak
	}
	defer release(&w.meter)
	if failed(w.meter.call(vMeterGetPeakValue, uintptr(unsafe.Pointer(&w.peak)), 0, 0, 0)) {
		w.peak = -1
	}
	return w.peak
}

// AudioSession describes one audio session for diagnostics.
type AudioSession struct {
	PID       uint32
	Exe, Path string
	Capture   bool    // a recording session; false: playback
	Active    bool    // recording or playing now
	Muted     bool    // the session or its device is muted in Windows
	Peak      float32 // level 0–1, -1 if unknown
}

// DumpAudioSessions calls fn for every capture and playback audio session,
// active or not, for diagnostics (`knockctl detect`).
func DumpAudioSessions(fn func(s AudioSession)) error {
	procs := &winProcesses{}
	if err := procs.refresh(); err != nil {
		return err
	}
	for _, flow := range []uintptr{eCapture, eRender} {
		w := &wasapi{flow: flow, withPeak: true}
		err := w.list(func(s audioSession, peak float32) {
			exe, path, _ := procs.process(s.pid)
			fn(AudioSession{PID: s.pid, Exe: exe, Path: path, Capture: flow == eCapture, Active: s.active, Muted: s.muted, Peak: peak})
		})
		if err != nil {
			return err
		}
	}
	return nil
}
