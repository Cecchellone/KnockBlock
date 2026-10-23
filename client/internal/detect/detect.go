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
	// Muted is true for a microphone app whose recording is muted in
	// Windows: its audio session, or the recording device it uses. An app
	// that keeps recording while muted inside a call is in a call with the
	// microphone off.
	Muted bool
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

// processes looks at the programs running now. The consent store says an
// app is using the microphone until the app tells Windows it stopped; an
// app that crashed or was killed never does, so its entry stays "in use"
// forever. Checking that the program still runs drops those entries.
type processes interface {
	// refresh takes a new snapshot of the running programs.
	refresh() error
	// running reports whether a program with this executable name
	// (lower-case, e.g. "discord.exe") runs, from this full path when path
	// is not empty. The path matters for apps that update themselves into
	// a new folder: an entry of Discord's previous version stays "in use"
	// while the new Discord.exe runs (docs/presence.md).
	running(exe, path string) bool
	// process returns the lower-case executable name and the full path of
	// a running process; path is empty when Windows does not tell it.
	process(pid uint32) (exe, path string, ok bool)
	// titles calls fn with the title of each visible window of the
	// programs with this executable name, until fn returns false.
	titles(exe string, fn func(title string) bool)
}

// captureSessions lists the programs recording audio now, from the Windows
// audio sessions (WASAPI). Unlike the consent store, a session ends when the
// program stops recording or exits, so it reflects real microphone use.
type captureSessions interface {
	// capture calls fn with every capture session, except the system
	// sounds session.
	capture(fn func(s audioSession)) error
	close() error
}

// audioSession is one program's audio session on one device.
type audioSession struct {
	pid    uint32
	active bool // recording now
	muted  bool // the session, or its device, is muted in Windows
}

// cameraStreams lists the programs streaming from a camera now.
type cameraStreams interface {
	streams(fn func(pid uint32)) error
	close() error
}

// consentStore turns consent store entries into a Snapshot. It is portable;
// only the walker, the process list and the audio sessions are
// Windows-specific.
type consentStore struct {
	w      walker
	audio  captureSessions        // nil: the microphone comes from the consent store
	camera cameraStreams          // nil: the camera comes from the consent store
	mutes  appMutes               // nil: only Windows' mute counts
	procs  processes              // nil: entries are trusted as they are
	polled bool                   // procs refreshed during this poll
	ignore map[string]bool        // lower-case exe or package names
	names  map[string]string      // lower-case exe or package name → friendly name
	cache  map[string]cachedEntry // by raw key, so steady polls allocate nothing
	dst    *Snapshot
	dev    Device
	// visitFn is c.visit, bound once: a method value allocates when taken.
	visitFn func(key []byte, packaged bool, start, stop uint64)

	self      uint32                 // this process, never reported
	recording []audioSession         // active capture sessions of this poll, reused
	byPID     map[uint32]cachedEntry // capture session and camera owners, by process ID
	captureFn func(s audioSession)

	streaming []uint32 // camera streams of this poll, reused
	relays    []App    // relay programs streaming from a camera this poll, reused
	streamFn  func(pid uint32)
}

type cachedEntry struct {
	app     App
	ignored bool
	exe     string // lower-case executable name of a desktop app
	path    string // full path of a desktop app ('\' separators), if known
	browser bool   // the app is a web browser
	relay   bool   // the app processes the camera or microphone for other apps
}

func newConsentStore(w walker, procs processes, opts Options) *consentStore {
	c := &consentStore{
		w:      w,
		procs:  procs,
		ignore: make(map[string]bool, len(builtinIgnore)+len(opts.Ignore)),
		names:  make(map[string]string, len(builtinNames)+len(opts.Names)),
		cache:  make(map[string]cachedEntry),
		byPID:  make(map[uint32]cachedEntry),
	}
	for _, name := range builtinIgnore {
		c.ignore[name] = true
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
	c.captureFn = c.addRecording
	c.streamFn = c.addStream
	return c
}

// withAudio makes the microphone come from the audio sessions; the consent
// store is still used for the camera, and for the microphone whenever the
// audio sessions cannot be read.
func (c *consentStore) withAudio(a captureSessions, self uint32) *consentStore {
	c.audio, c.self = a, self
	return c
}

// withAppMutes makes the mute buttons of apps that keep recording while
// muted (Discord) count as a muted microphone.
func (c *consentStore) withAppMutes(m appMutes) *consentStore {
	c.mutes = m
	return c
}

// withCamera makes the camera come from the camera streams; the consent
// store is the fallback when they cannot be read.
func (c *consentStore) withCamera(cs cameraStreams, self uint32) *consentStore {
	c.camera, c.self = cs, self
	return c
}

func (c *consentStore) Poll(dst *Snapshot) error {
	dst.Reset()
	c.dst = dst
	c.polled = false
	micDone := c.audio != nil && c.pollAudio() == nil
	camDone := c.camera != nil && c.pollCamera() == nil
	for dev := Microphone; dev <= Camera; dev++ {
		if (dev == Microphone && micDone) || (dev == Camera && camDone) {
			continue
		}
		c.dev = dev
		if err := c.w.walk(dev, c.visitFn); err != nil {
			return err
		}
	}
	if camDone {
		c.relayedCamera()
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
	app := e.app
	if e.exe != "" && c.procs != nil {
		// Only now, with an app in use, look at the running programs: a
		// steady poll with nothing in use costs nothing more.
		if !c.polled {
			c.polled = c.procs.refresh() == nil
		}
		if c.polled && !c.procs.running(e.exe, e.path) {
			return // stale entry: the program is gone
		}
		if c.polled && e.browser {
			app = c.webApp(app, e.exe)
		}
	}
	if c.dev == Camera {
		c.dst.Camera = append(c.dst.Camera, app)
	} else {
		c.dst.Mic = append(c.dst.Mic, app)
	}
}

// webApp names the web app a browser is running, from its window titles:
// "Teams (Firefox)" for a Teams call in Firefox. The browser name stays when
// no window title names a known web app.
func (c *consentStore) webApp(browser App, exe string) App {
	var found string
	c.procs.titles(exe, func(title string) bool {
		found = WebAppFromTitle(title)
		return found == ""
	})
	if found != "" {
		browser.Name, browser.Known = found+" ("+browser.Name+")", true
	}
	return browser
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
	e := cachedEntry{app: app, ignored: c.ignore[lower]}
	if !packaged {
		e.exe, e.browser, e.relay = lower, browsers[lower], slices.Contains(builtinIgnore, lower)
		if strings.IndexByte(key, '#') >= 0 {
			e.path = strings.ReplaceAll(key, "#", `\`)
		}
	}
	return e
}

// pollAudio fills dst.Mic from the active capture sessions. An error means
// the sessions could not be read; dst.Mic is then left empty.
func (c *consentStore) pollAudio() error {
	c.recording = c.recording[:0]
	if err := c.audio.capture(c.captureFn); err != nil {
		return err
	}
	if len(c.recording) == 0 || !c.refreshProcs() {
		return nil
	}
	for _, rec := range c.recording {
		e, ok := c.entryForPID(rec.pid)
		if !ok || e.ignored {
			continue
		}
		muted := rec.muted
		if !muted && c.mutes != nil {
			muted, _ = c.mutes.muted(e.exe) // muted inside the app (Discord)
		}
		// A program can hold several sessions, on several devices: it is
		// muted only if every one of them is.
		if i := indexKey(c.dst.Mic, e.app.Key); i >= 0 {
			c.dst.Mic[i].Muted = c.dst.Mic[i].Muted && muted
			continue
		}
		app := e.app
		app.Muted = muted
		if e.browser {
			app = c.webApp(app, e.exe)
		}
		c.dst.Mic = append(c.dst.Mic, app)
	}
	c.trimPIDs(len(c.recording))
	return nil
}

// pollCamera fills dst.Camera from the camera streams. Relay programs
// (NVIDIA Broadcast) stream the camera for an app that reads their virtual
// camera; they are kept aside for relayedCamera.
func (c *consentStore) pollCamera() error {
	c.streaming, c.relays = c.streaming[:0], c.relays[:0]
	if err := c.camera.streams(c.streamFn); err != nil {
		return err
	}
	if len(c.streaming) == 0 || !c.refreshProcs() {
		return nil
	}
	for _, pid := range c.streaming {
		e, ok := c.entryForPID(pid)
		switch {
		case !ok:
		case e.relay:
			if indexKey(c.relays, e.app.Key) < 0 {
				c.relays = append(c.relays, e.app)
			}
		case e.ignored:
		case indexKey(c.dst.Camera, e.app.Key) < 0:
			app := e.app
			if e.browser {
				app = c.webApp(app, e.exe)
			}
			c.dst.Camera = append(c.dst.Camera, app)
		}
	}
	c.trimPIDs(len(c.streaming))
	return nil
}

// relayedCamera reports the camera streamed by a relay program. Windows
// does not tell which app reads the relay's virtual camera; an app in a
// call is the likely one, so the microphone apps are reported, or the
// relay itself when none records.
func (c *consentStore) relayedCamera() {
	if len(c.relays) == 0 || len(c.dst.Camera) > 0 {
		return
	}
	if len(c.dst.Mic) > 0 {
		for _, app := range c.dst.Mic {
			app.Muted = false
			c.dst.Camera = append(c.dst.Camera, app)
		}
		return
	}
	c.dst.Camera = append(c.dst.Camera, c.relays...)
}

// refreshProcs looks at the running programs, once per poll.
func (c *consentStore) refreshProcs() bool {
	if c.procs == nil {
		return false
	}
	if !c.polled {
		c.polled = c.procs.refresh() == nil
	}
	return c.polled
}

// entryForPID describes a running process, cached by process ID.
func (c *consentStore) entryForPID(pid uint32) (cachedEntry, bool) {
	exe, path, ok := c.procs.process(pid)
	if !ok {
		return cachedEntry{}, false // exited meanwhile
	}
	e, cached := c.byPID[pid]
	if !cached || e.exe != exe {
		key := exe
		if path != "" {
			key = strings.ReplaceAll(path, `\`, "#") // as the consent store writes it
		}
		e = c.describe(key, false)
		c.byPID[pid] = e
	}
	return e, true
}

// trimPIDs drops exited processes from byPID now and then.
func (c *consentStore) trimPIDs(live int) {
	if len(c.byPID) > 4*live+16 {
		clear(c.byPID)
	}
}

func (c *consentStore) addRecording(s audioSession) {
	if s.active && s.pid != 0 && s.pid != c.self {
		c.recording = append(c.recording, s)
	}
}

func (c *consentStore) addStream(pid uint32) {
	if pid != 0 && pid != c.self && !slices.Contains(c.streaming, pid) {
		c.streaming = append(c.streaming, pid)
	}
}

func indexKey(apps []App, key string) int {
	for i := range apps {
		if apps[i].Key == key {
			return i
		}
	}
	return -1
}

func (c *consentStore) Close() error {
	if c.audio != nil {
		c.audio.close()
	}
	if c.camera != nil {
		c.camera.close()
	}
	return c.w.close()
}

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
