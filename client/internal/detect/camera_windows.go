//go:build windows

package detect

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sensorMonitor tracks the programs streaming from a camera, from Media
// Foundation's sensor activity reports (IMFSensorActivityMonitor, Windows
// 10 1809 and later, declared in the Windows SDK header mfidl.h). Windows
// sends a report whenever a program opens, starts, stops or closes a
// camera; each report lists every camera in use and its programs.
//
// The consent store no longer says when the camera is used on recent
// Windows 11 builds (usage moved to a database only administrators can
// read, docs/detection-notes.md), and it never saw programs that read a
// virtual camera, such as NVIDIA Broadcast's.
//
// The monitor runs on its own OS thread in the multithreaded apartment;
// reports arrive on Media Foundation's threads and are copied under mu.
type sensorMonitor struct {
	mu     sync.Mutex
	active []cameraStream // programs streaming now, or until recently

	cb   sensorCallback
	stop chan struct{}
	done chan struct{}
}

var (
	mfplat                            = windows.NewLazySystemDLL("mfplat.dll")
	procMFStartup                     = mfplat.NewProc("MFStartup")
	procMFShutdown                    = mfplat.NewProc("MFShutdown")
	procMFCreateSensorActivityMonitor = windows.NewLazySystemDLL("mfsensorgroup.dll").NewProc("MFCreateSensorActivityMonitor")

	iidIUnknown                       = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIMFSensorActivitiesReportCallb = windows.GUID{Data1: 0xDE5072EE, Data2: 0xDBE3, Data3: 0x46DC, Data4: [8]byte{0x8A, 0x87, 0xB6, 0xF6, 0x31, 0x19, 0x47, 0x51}}
)

const (
	mfVersion       = 0x00020070 // MF_VERSION: MF_SDK_VERSION 2, MF_API_VERSION 0x70
	mfStartupLite   = 1          // MFSTARTUP_LITE: no sockets
	eNoInterface    = 0x80004002
	vMonitorStart   = 3 // IMFSensorActivityMonitor
	vMonitorStop    = 4 //
	vReportsCount   = 3 // IMFSensorActivitiesReport
	vReportsItem    = 4 //
	vSensorProcs    = 5 // IMFSensorActivityReport (after GetFriendlyName, GetSymbolicLink)
	vSensorProcItem = 6 //
	vProcPID        = 3 // IMFSensorProcessActivity
	vProcStreaming  = 4 //
)

// cameraStream is a program streaming from a camera.
type cameraStream struct {
	pid   uint32
	live  bool      // in the last report
	until time.Time // when not live: still reported until then
}

// streamHold keeps reporting a program for a moment after a report leaves
// it out. During a Discord call with the camera on, reports left the camera
// out for a second or two now and then, and the light jumped between
// "call" and "mic" (owner's PC, October 2026).
const streamHold = 5 * time.Second

// sensorCallback is our IMFSensorActivitiesReportCallback: a COM object
// whose first field is its vtable. Windows only calls it, never frees it.
type sensorCallback struct {
	vtbl *[4]uintptr
	m    *sensorMonitor
}

// One vtable serves every monitor: callbacks made with NewCallback are
// never freed, so they are made once.
var (
	sensorVtblOnce sync.Once
	sensorVtbl     [4]uintptr
)

func initSensorVtbl() {
	sensorVtbl[0] = syscall.NewCallback(func(this *sensorCallback, riid *windows.GUID, out *unsafe.Pointer) uintptr {
		if *riid == iidIUnknown || *riid == iidIMFSensorActivitiesReportCallb {
			*out = unsafe.Pointer(this)
			return 0
		}
		*out = nil
		return eNoInterface
	})
	// The object lives as long as its sensorMonitor: no reference counting.
	sensorVtbl[1] = syscall.NewCallback(func(this *sensorCallback) uintptr { return 1 })
	sensorVtbl[2] = syscall.NewCallback(func(this *sensorCallback) uintptr { return 1 })
	sensorVtbl[3] = syscall.NewCallback(func(this *sensorCallback, reports *comObject) uintptr {
		this.m.onReport(reports)
		return 0
	})
}

// newSensorMonitor starts the monitor and waits until it runs or fails.
func newSensorMonitor() (*sensorMonitor, error) {
	if err := procMFCreateSensorActivityMonitor.Find(); err != nil {
		return nil, fmt.Errorf("detect: camera monitor not available: %w", err)
	}
	sensorVtblOnce.Do(initSensorVtbl)
	m := &sensorMonitor{stop: make(chan struct{}), done: make(chan struct{})}
	m.cb = sensorCallback{vtbl: &sensorVtbl, m: m}
	started := make(chan error, 1)
	go m.run(started)
	if err := <-started; err != nil {
		<-m.done
		return nil, err
	}
	return m, nil
}

func (m *sensorMonitor) run(started chan<- error) {
	defer close(m.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err {
	case nil, syscall.Errno(sFalse):
		defer windows.CoUninitialize()
	default:
		started <- fmt.Errorf("detect: CoInitializeEx: %w", err)
		return
	}
	if hr, _, _ := procMFStartup.Call(mfVersion, mfStartupLite); failed(uint32(hr)) {
		started <- fmt.Errorf("detect: MFStartup: HRESULT %#x", uint32(hr))
		return
	}
	defer procMFShutdown.Call()
	var mon *comObject
	if hr, _, _ := procMFCreateSensorActivityMonitor.Call(uintptr(unsafe.Pointer(&m.cb)), uintptr(unsafe.Pointer(&mon))); failed(uint32(hr)) {
		started <- fmt.Errorf("detect: creating the camera monitor: HRESULT %#x", uint32(hr))
		return
	}
	defer release(&mon)
	if hr := mon.call(vMonitorStart, 0, 0, 0, 0); failed(hr) {
		started <- fmt.Errorf("detect: starting the camera monitor: HRESULT %#x", hr)
		return
	}
	started <- nil
	<-m.stop
	mon.call(vMonitorStop, 0, 0, 0, 0) // waits for a report in progress
}

// onReport copies the streaming programs of every camera in the report.
// It runs on a Media Foundation thread.
func (m *sensorMonitor) onReport(reports *comObject) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for i := range m.active {
		m.active[i].live = false
	}
	defer func() {
		for i := range m.active {
			if st := &m.active[i]; !st.live && st.until.IsZero() {
				st.until = now.Add(streamHold)
			}
		}
	}()
	var count, procs, pid, streaming uint32
	if failed(reports.call(vReportsCount, uintptr(unsafe.Pointer(&count)), 0, 0, 0)) {
		return
	}
	for i := uint32(0); i < count; i++ {
		var sensor *comObject
		if failed(reports.call(vReportsItem, uintptr(i), uintptr(unsafe.Pointer(&sensor)), 0, 0)) {
			continue
		}
		if !failed(sensor.call(vSensorProcs, uintptr(unsafe.Pointer(&procs)), 0, 0, 0)) {
			for j := uint32(0); j < procs; j++ {
				var p *comObject
				if failed(sensor.call(vSensorProcItem, uintptr(j), uintptr(unsafe.Pointer(&p)), 0, 0)) {
					continue
				}
				// A program that opened a camera without streaming (a
				// settings page reading its formats) does not count.
				if !failed(p.call(vProcPID, uintptr(unsafe.Pointer(&pid)), 0, 0, 0)) &&
					!failed(p.call(vProcStreaming, uintptr(unsafe.Pointer(&streaming)), 0, 0, 0)) &&
					streaming != 0 {
					m.markLive(pid)
				}
				release(&p)
			}
		}
		release(&sensor)
	}
}

// markLive records pid as streaming in the current report; m.mu is held.
func (m *sensorMonitor) markLive(pid uint32) {
	for i := range m.active {
		if m.active[i].pid == pid {
			m.active[i] = cameraStream{pid: pid, live: true}
			return
		}
	}
	m.active = append(m.active, cameraStream{pid: pid, live: true})
}

// streams calls fn with every program streaming from a camera now, or
// left out of the reports less than streamHold ago. Until the first report
// arrives nothing streams: Windows sends one at start only when a camera
// is in use.
func (m *sensorMonitor) streams(fn func(pid uint32)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	kept := m.active[:0]
	for _, st := range m.active {
		if st.live || now.Before(st.until) {
			kept = append(kept, st)
			fn(st.pid)
		}
	}
	m.active = kept
	return nil
}

func (m *sensorMonitor) close() error {
	select {
	case <-m.stop:
		return errors.New("detect: camera monitor already closed")
	default:
	}
	close(m.stop)
	<-m.done
	return nil
}

// DumpCameraStreams calls fn for every program streaming from a camera,
// for diagnostics (`knockctl detect`). It watches the cameras for wait
// first, as reports arrive only on changes.
func DumpCameraStreams(wait func(), fn func(pid uint32, exe, path string)) error {
	m, err := newSensorMonitor()
	if err != nil {
		return err
	}
	defer m.close()
	wait()
	procs := &winProcesses{}
	if err := procs.refresh(); err != nil {
		return err
	}
	return m.streams(func(pid uint32) {
		exe, path, _ := procs.process(pid)
		fn(pid, exe, path)
	})
}
