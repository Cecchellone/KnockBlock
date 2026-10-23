//go:build windows

package detect

import (
	"errors"
	"os"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Registry paths under HKEY_CURRENT_USER (CLAUDE.md §5.2). Store apps are
// subkeys of the device key; desktop apps are subkeys of NonPackaged.
const consentStorePath = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\`

var (
	devicePaths = [2]*uint16{
		Microphone: utf16Ptr(consentStorePath + "microphone"),
		Camera:     utf16Ptr(consentStorePath + "webcam"),
	}
	nonPackagedPaths = [2]*uint16{
		Microphone: utf16Ptr(consentStorePath + `microphone\NonPackaged`),
		Camera:     utf16Ptr(consentStorePath + `webcam\NonPackaged`),
	}
	valueStart = utf16Ptr("LastUsedTimeStart")
	valueStop  = utf16Ptr("LastUsedTimeStop")
)

// utf16Ptr converts a constant without NUL characters; the error case
// cannot happen.
func utf16Ptr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// NewConsentStore returns a Detector that reads the audio sessions (WASAPI)
// for the microphone and Media Foundation's camera reports for the camera,
// with the Windows consent store as the fallback for either.
func NewConsentStore(opts Options) (Detector, error) {
	self := windows.GetCurrentProcessId()
	c := newConsentStore(&registryWalker{}, &winProcesses{}, opts).withAudio(&wasapi{}, self)
	if appData, err := os.UserConfigDir(); err == nil {
		c.withAppMutes(newDiscordMutes(appData))
	}
	if cam, err := newSensorMonitor(); err == nil {
		c.withCamera(cam, self)
	}
	return c, nil
}

// registryWalker enumerates the consent store with raw registry calls and
// reused buffers, so a poll allocates nothing once every key is cached.
// Keys are opened on every poll: cheap, and new device keys are picked up.
type registryWalker struct {
	name [256]uint16 // registry key names are at most 255 characters
	key  []byte      // name as UTF-8
}

func (w *registryWalker) walk(dev Device, fn func(key []byte, packaged bool, start, stop uint64)) error {
	if err := w.walkKey(devicePaths[dev], true, fn); err != nil {
		return err
	}
	return w.walkKey(nonPackagedPaths[dev], false, fn)
}

func (w *registryWalker) walkKey(path *uint16, packaged bool, fn func([]byte, bool, uint64, uint64)) error {
	var parent windows.Handle
	err := windows.RegOpenKeyEx(windows.HKEY_CURRENT_USER, path, 0, windows.KEY_ENUMERATE_SUB_KEYS, &parent)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil // no app has used this device yet
	}
	if err != nil {
		return err
	}
	defer windows.RegCloseKey(parent)

	for i := uint32(0); ; i++ {
		n := uint32(len(w.name))
		err := windows.RegEnumKeyEx(parent, i, &w.name[0], &n, nil, nil, nil, nil)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return nil
		}
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			continue // cannot happen for valid names; skip
		}
		if err != nil {
			return err
		}
		name := w.name[:n]
		if packaged && equalASCII(name, "NonPackaged") {
			continue // the container of desktop apps, walked separately
		}
		start, stop, ok := readTimes(parent, &w.name[0])
		if !ok {
			continue
		}
		w.key = appendUTF16(w.key[:0], name)
		fn(w.key, packaged, start, stop)
	}
}

// readTimes reads LastUsedTimeStart and LastUsedTimeStop of a subkey.
func readTimes(parent windows.Handle, name *uint16) (start, stop uint64, ok bool) {
	var k windows.Handle
	if windows.RegOpenKeyEx(parent, name, 0, windows.KEY_QUERY_VALUE, &k) != nil {
		return 0, 0, false
	}
	defer windows.RegCloseKey(k)
	return readQword(k, valueStart), readQword(k, valueStop), true
}

// readQword returns a REG_QWORD value, or 0 if it is missing.
func readQword(k windows.Handle, name *uint16) uint64 {
	var v uint64
	var typ uint32
	size := uint32(unsafe.Sizeof(v))
	if windows.RegQueryValueEx(k, name, nil, &typ, (*byte)(unsafe.Pointer(&v)), &size) != nil ||
		typ != windows.REG_QWORD {
		return 0
	}
	return v
}

func (w *registryWalker) close() error { return nil }

// appendUTF16 appends s, UTF-16, to dst as UTF-8 without allocating
// (beyond growing dst).
func appendUTF16(dst []byte, s []uint16) []byte {
	for i := 0; i < len(s); i++ {
		r := rune(s[i])
		if utf16.IsSurrogate(r) && i+1 < len(s) {
			r = utf16.DecodeRune(r, rune(s[i+1]))
			i++
		}
		dst = utf8.AppendRune(dst, r)
	}
	return dst
}

func equalASCII(s []uint16, ascii string) bool {
	if len(s) != len(ascii) {
		return false
	}
	for i := range s {
		if s[i] != uint16(ascii[i]) {
			return false
		}
	}
	return true
}

// DumpConsentStore calls fn for every consent store entry, in use or not,
// for diagnostics (`knockctl detect`).
func DumpConsentStore(fn func(dev Device, key string, packaged bool, start, stop uint64)) error {
	w := &registryWalker{}
	for dev := Microphone; dev <= Camera; dev++ {
		err := w.walk(dev, func(key []byte, packaged bool, start, stop uint64) {
			fn(dev, string(key), packaged, start, stop)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
