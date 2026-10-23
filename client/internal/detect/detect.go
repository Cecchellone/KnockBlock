package detect

import (
	"errors"
	"slices"
	"strings"
)

// Device is the hardware an app can use.
type Device int

// Devices reported by the consent store.
const (
	Microphone Device = iota
	Camera
)

func (d Device) String() string {
	if d == Camera {
		return "camera"
	}
	return "mic"
}

// App is an app that is using a device right now.
type App struct {
	// Key is the consent store entry: a package family name for Store apps
	// (e.g. "MSTeams_8wekyb3d8bbwe"), or the executable path with '#'
	// instead of '\' for desktop apps ("C:#Program Files#…#Zoom.exe").
	Key string
	// Exe is the executable file name of a desktop app ("Zoom.exe"); empty
	// for Store apps.
	Exe string
	// Name is the friendly name shown on the device ("Zoom").
	Name string
	// Known is true when Name comes from the built-in table or an override.
	Known bool
}

// Snapshot lists the apps using each device. Detector.Poll reuses its
// slices, so a Snapshot must not be shared across polls.
type Snapshot struct {
	Mic    []App
	Camera []App
}

// Reset empties the snapshot, keeping its capacity.
func (s *Snapshot) Reset() {
	s.Mic = s.Mic[:0]
	s.Camera = s.Camera[:0]
}

// Detector reports which apps use the microphone and the camera.
type Detector interface {
	// Poll fills dst, reusing its slices.
	Poll(dst *Snapshot) error
	Close() error
}

// ErrUnsupported is returned where the platform has no detector.
var ErrUnsupported = errors.New("detect: not supported on this platform")

// Options configures a consent store detector.
type Options struct {
	// Ignore lists executable names ("obs64.exe") or Store package names
	// whose use is not reported, compared case-insensitively.
	Ignore []string
	// Names maps executable or package names to friendly names, on top of
	// (and overriding) the built-in table.
	Names map[string]string
}

// walker visits every consent store entry of one device: the Store apps,
// then the desktop apps (the NonPackaged subkey). key is only valid during
// the call.
type walker interface {
	walk(dev Device, fn func(key []byte, packaged bool, start, stop uint64)) error
	close() error
}

// consentStore turns consent store entries into a Snapshot. It is portable;
// only the walker is Windows-specific.
type consentStore struct {
	w      walker
	ignore map[string]bool        // lower-case exe or package names
	names  map[string]string      // lower-case exe or package name → friendly name
	cache  map[string]cachedEntry // by raw key, so steady polls allocate nothing
	dst    *Snapshot
	dev    Device
	// visitFn is c.visit, bound once: a method value allocates when taken.
	visitFn func(key []byte, packaged bool, start, stop uint64)
}

type cachedEntry struct {
	app     App
	ignored bool
}

func newConsentStore(w walker, opts Options) *consentStore {
	c := &consentStore{
		w:      w,
		ignore: make(map[string]bool, len(opts.Ignore)),
		names:  make(map[string]string, len(builtinNames)+len(opts.Names)),
		cache:  make(map[string]cachedEntry),
	}
	for _, name := range opts.Ignore {
		c.ignore[strings.ToLower(name)] = true
	}
	for k, v := range builtinNames {
		c.names[k] = v
	}
	for k, v := range opts.Names {
		c.names[strings.ToLower(k)] = v
	}
	c.visitFn = c.visit
	return c
}

func (c *consentStore) Poll(dst *Snapshot) error {
	dst.Reset()
	c.dst = dst
	for dev := Microphone; dev <= Camera; dev++ {
		c.dev = dev
		if err := c.w.walk(dev, c.visitFn); err != nil {
			return err
		}
	}
	// Map iteration order does not matter; the walker order does, so sort
	// for stable results.
	sortApps(dst.Mic)
	sortApps(dst.Camera)
	return nil
}

func (c *consentStore) visit(key []byte, packaged bool, start, stop uint64) {
	if !InUse(start, stop) {
		return
	}
	e, ok := c.cache[string(key)] // no allocation for the lookup
	if !ok {
		e = c.describe(string(key), packaged)
		c.cache[string(key)] = e
	}
	if e.ignored {
		return
	}
	if c.dev == Camera {
		c.dst.Camera = append(c.dst.Camera, e.app)
	} else {
		c.dst.Mic = append(c.dst.Mic, e.app)
	}
}

// describe builds the App for a consent store key, once per key.
func (c *consentStore) describe(key string, packaged bool) cachedEntry {
	app := App{Key: key}
	id := key
	if !packaged {
		app.Exe = ExeFromKey(key)
		id = app.Exe
	}
	lower := strings.ToLower(id)
	if name, ok := c.names[lower]; ok {
		app.Name, app.Known = name, true
	} else {
		app.Name = fallbackName(id, packaged)
	}
	return cachedEntry{app: app, ignored: c.ignore[lower]}
}

func (c *consentStore) Close() error { return c.w.close() }

// InUse reports whether a consent store entry is in use now: the app
// started using the device and has not stopped (CLAUDE.md §5.2).
func InUse(start, stop uint64) bool { return start != 0 && stop == 0 }

// ExeFromKey returns the executable name from a NonPackaged key, where the
// path separators are '#': "C:#Users#me#AppData#Local#Discord#app-1.0#Discord.exe"
// → "Discord.exe".
func ExeFromKey(key string) string {
	if i := strings.LastIndexByte(key, '#'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// fallbackName is the name shown for apps not in the table: the executable
// without ".exe", or for Store apps the package name without the publisher
// hash ("Microsoft.WindowsCamera_8wekyb3d8bbwe" → "Microsoft.WindowsCamera").
func fallbackName(id string, packaged bool) string {
	if packaged {
		if i := strings.LastIndexByte(id, '_'); i > 0 {
			return id[:i]
		}
		return id
	}
	if len(id) > 4 && strings.EqualFold(id[len(id)-4:], ".exe") {
		return id[:len(id)-4]
	}
	return id
}

// sortApps orders apps by name so results do not depend on registry order.
func sortApps(apps []App) {
	slices.SortFunc(apps, func(a, b App) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
}
