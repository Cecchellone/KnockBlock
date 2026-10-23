//go:build windows

package sysevents

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	powrprof                                     = windows.NewLazySystemDLL("powrprof.dll")
	procPowerRegisterSuspendResumeNotification   = powrprof.NewProc("PowerRegisterSuspendResumeNotification")
	procPowerUnregisterSuspendResumeNotification = powrprof.NewProc("PowerUnregisterSuspendResumeNotification")
)

const (
	deviceNotifyCallback  = 2    // DEVICE_NOTIFY_CALLBACK
	pbtAPMResumeSuspend   = 0x7  // PBT_APMRESUMESUSPEND
	pbtAPMResumeAutomatic = 0x12 // PBT_APMRESUMEAUTOMATIC
	afUnspec              = 0
)

// deviceNotifySubscribeParameters is DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS.
type deviceNotifySubscribeParameters struct {
	callback uintptr
	context  uintptr
}

// Windows keeps pointers to these, and callbacks created with NewCallback
// are never freed, so they are created once per process.
var (
	mu        sync.Mutex
	current   *debounce
	powerOnce sync.Once
	powerCB   uintptr
	netCB     uintptr
	params    deviceNotifySubscribeParameters
)

// Watch calls fn with a short reason on resume from sleep and on network
// changes (debounced by 2 s), until stop is called. Only one Watch may be
// active at a time.
func Watch(fn func(reason string)) (stop func(), err error) {
	powerOnce.Do(func() {
		powerCB = windows.NewCallback(func(context, typ, setting uintptr) uintptr {
			if typ == pbtAPMResumeSuspend || typ == pbtAPMResumeAutomatic {
				notify("resume")
			}
			return 0
		})
		netCB = windows.NewCallback(func(context, row, notificationType uintptr) uintptr {
			notify("network")
			return 0
		})
		params = deviceNotifySubscribeParameters{callback: powerCB}
	})
	b := &debounce{d: 2 * time.Second, fn: fn}
	mu.Lock()
	current = b
	mu.Unlock()

	var powerHandle uintptr
	if r, _, _ := procPowerRegisterSuspendResumeNotification.Call(
		deviceNotifyCallback, uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&powerHandle))); r != 0 {
		powerHandle = 0 // not fatal: network changes still work
	}
	var netHandle windows.Handle
	netErr := windows.NotifyIpInterfaceChange(afUnspec, netCB, nil, false, &netHandle)

	return func() {
		if powerHandle != 0 {
			procPowerUnregisterSuspendResumeNotification.Call(powerHandle)
		}
		if netErr == nil {
			windows.CancelMibChangeNotify2(netHandle)
		}
		mu.Lock()
		current = nil
		mu.Unlock()
		b.stop()
	}, nil
}

func notify(reason string) {
	mu.Lock()
	b := current
	mu.Unlock()
	if b != nil {
		b.trigger(reason)
	}
}
