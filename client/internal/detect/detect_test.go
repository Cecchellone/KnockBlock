package detect

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeWalker serves fixed consent store entries.
type fakeWalker struct {
	entries map[Device][]fakeEntry
	walks   int
}

type fakeEntry struct {
	key         string
	raw         []byte
	packaged    bool
	start, stop uint64
}

func (w *fakeWalker) walk(dev Device, fn func([]byte, bool, uint64, uint64)) error {
	w.walks++
	for i := range w.entries[dev] {
		e := &w.entries[dev][i]
		if e.raw == nil {
			e.raw = []byte(e.key) // once, like the real walker's reused buffer
		}
		fn(e.raw, e.packaged, e.start, e.stop)
	}
	return nil
}

func (w *fakeWalker) close() error { return nil }

func names(apps []App) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}

func TestInUse(t *testing.T) {
	tests := []struct {
		start, stop uint64
		want        bool
	}{
		{0, 0, false},     // never used
		{100, 0, true},    // started, not stopped
		{100, 200, false}, // used earlier
	}
	for _, tt := range tests {
		if got := InUse(tt.start, tt.stop); got != tt.want {
			t.Errorf("InUse(%d, %d) = %v", tt.start, tt.stop, got)
		}
	}
}

func TestExeFromKey(t *testing.T) {
	tests := map[string]string{
		`C:#Users#me#AppData#Local#Discord#app-1.0.9#Discord.exe`: "Discord.exe",
		`C:#Program Files#Zoom#bin#Zoom.exe`:                      "Zoom.exe",
		"plain.exe":                                               "plain.exe",
	}
	for key, want := range tests {
		if got := ExeFromKey(key); got != want {
			t.Errorf("ExeFromKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestPoll(t *testing.T) {
	w := &fakeWalker{entries: map[Device][]fakeEntry{
		Microphone: {
			{key: `C:#Users#me#AppData#Local#Discord#app-1#Discord.exe`, start: 5},
			{key: `C:#Program Files#Old#Old.exe`, start: 5, stop: 6}, // stopped
			{key: "MSTeams_8wekyb3d8bbwe", packaged: true, start: 5},
			{key: `C:#Tools#nvidia broadcast.exe`, start: 5},        // ignored
			{key: `C:#Tools#MyRecorder.EXE`, start: 5},              // unknown
			{key: "Contoso.Notes_abc123", packaged: true, start: 5}, // unknown package
		},
		Camera: {
			{key: `C:#Program Files#Zoom#bin#Zoom.exe`, start: 7},
		},
	}}
	d := newConsentStore(w, nil, Options{
		Ignore: []string{"NVIDIA Broadcast.exe"},
		Names:  map[string]string{"zoom.exe": "Zoom Meetings"}, // overrides the table
	})
	var s Snapshot
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Mic), []string{"Contoso.Notes", "Discord", "MyRecorder", "Teams"}; !slices.Equal(got, want) {
		t.Errorf("mic = %v, want %v", got, want)
	}
	if got := names(s.Camera); !slices.Equal(got, []string{"Zoom Meetings"}) {
		t.Errorf("camera = %v", got)
	}
	for _, a := range s.Mic {
		switch a.Name {
		case "Discord", "Teams":
			if !a.Known {
				t.Errorf("%s should be known", a.Name)
			}
		case "MyRecorder":
			if a.Known || a.Exe != "MyRecorder.EXE" {
				t.Errorf("unknown app %+v", a)
			}
		}
	}

	// Steady state: polling again reuses the snapshot and allocates nothing.
	allocs := testing.AllocsPerRun(100, func() {
		if err := d.Poll(&s); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Errorf("steady poll allocates %.0f times", allocs)
	}
}

func TestFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.txt")
	d := FromFile(path, Options{})
	var s Snapshot
	if err := d.Poll(&s); err != nil || len(s.Mic)+len(s.Camera) != 0 {
		t.Fatalf("missing file: %v, %+v", err, s)
	}
	os.WriteFile(path, []byte("# comment\nmic Discord.exe\ncamera MSTeams_8wekyb3d8bbwe\n\n"), 0o644)
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names(s.Mic), []string{"Discord"}) || !slices.Equal(names(s.Camera), []string{"Teams"}) {
		t.Fatalf("got %+v", s)
	}
	os.WriteFile(path, []byte("speaker x.exe\n"), 0o644)
	if err := d.Poll(&s); err == nil {
		t.Fatal("bad line accepted")
	}
}

// fakeProcs is a fixed process list with window titles.
type fakeProcs struct {
	titles_  map[string][]string // lower-case exe → window titles
	paths    map[string]string   // lower-case exe → full path, if known
	pids     map[uint32]string   // process ID → lower-case exe
	refreshs int
}

func (p *fakeProcs) refresh() error { p.refreshs++; return nil }
func (p *fakeProcs) running(exe, path string) bool {
	_, ok := p.titles_[exe]
	if got := p.paths[exe]; ok && path != "" && got != "" {
		return strings.EqualFold(got, path)
	}
	return ok
}
func (p *fakeProcs) process(pid uint32) (string, string, bool) {
	exe, ok := p.pids[pid]
	return exe, p.paths[exe], ok
}
func (p *fakeProcs) titles(exe string, fn func(string) bool) {
	for _, t := range p.titles_[exe] {
		if !fn(t) {
			return
		}
	}
}

func TestStaleEntriesAndWebApps(t *testing.T) {
	w := &fakeWalker{entries: map[Device][]fakeEntry{
		Microphone: {
			// Discord crashed long ago: its entry still says "in use".
			{key: `C:#Users#me#AppData#Local#Discord#app-1#Discord.exe`, start: 5},
			{key: `C:#Program Files#Mozilla Firefox#firefox.exe`, start: 5},
			{key: `C:#Program Files#Google#Chrome#Application#chrome.exe`, start: 5},
			{key: "MSTeams_8wekyb3d8bbwe", packaged: true, start: 5}, // Store apps are not checked
		},
	}}
	procs := &fakeProcs{titles_: map[string][]string{
		"firefox.exe": {"Mozilla Firefox", "Chat | Microsoft Teams — Mozilla Firefox"},
		"chrome.exe":  {"Inbox - Gmail - Google Chrome"},
	}}
	d := newConsentStore(w, procs, Options{})
	var s Snapshot
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Mic), []string{"Chrome", "Teams", "Teams (Firefox)"}; !slices.Equal(got, want) {
		t.Errorf("mic = %v, want %v", got, want)
	}
	if procs.refreshs != 1 {
		t.Errorf("%d process snapshots in one poll, want 1", procs.refreshs)
	}
	// Nothing in use: no process snapshot at all.
	d = newConsentStore(&fakeWalker{}, procs, Options{})
	d.Poll(&s)
	if procs.refreshs != 1 {
		t.Error("process snapshot taken with nothing in use")
	}
}

func TestOldVersionEntries(t *testing.T) {
	// Discord updates itself into a new folder; the entry of an old version
	// says "in use" forever, and the new Discord.exe must not keep it alive.
	w := &fakeWalker{entries: map[Device][]fakeEntry{
		Microphone: {{key: `C:#Users#me#AppData#Local#Discord#app-1.0.9254#Discord.exe`, start: 5}},
	}}
	tests := []struct {
		name, path string
		want       []string
	}{
		{"new version runs", `C:\Users\me\AppData\Local\Discord\app-1.0.9261\Discord.exe`, nil},
		{"that version runs", `c:\users\me\appdata\local\discord\app-1.0.9254\discord.exe`, []string{"Discord"}},
		{"path unknown", "", []string{"Discord"}},
	}
	for _, tt := range tests {
		procs := &fakeProcs{
			titles_: map[string][]string{"discord.exe": nil},
			paths:   map[string]string{"discord.exe": tt.path},
		}
		var s Snapshot
		if err := newConsentStore(w, procs, Options{}).Poll(&s); err != nil {
			t.Fatal(err)
		}
		if got := names(s.Mic); !slices.Equal(got, tt.want) {
			t.Errorf("%s: mic = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// fakeAudio serves fixed capture sessions.
type fakeAudio struct {
	sessions []fakeSession
	err      error
}

type fakeSession = audioSession

func (a *fakeAudio) capture(fn func(audioSession)) error {
	for _, s := range a.sessions {
		fn(s)
	}
	return a.err
}

func (a *fakeAudio) close() error { return nil }

func TestAudioSessions(t *testing.T) {
	// The consent store says Discord and Teams use the microphone; the
	// audio sessions are what counts.
	w := &fakeWalker{entries: map[Device][]fakeEntry{
		Microphone: {
			{key: `C:#Users#me#AppData#Local#Discord#app-1#Discord.exe`, start: 5},
			{key: "MSTeams_8wekyb3d8bbwe", packaged: true, start: 5},
		},
		Camera: {{key: `C:#Program Files#Zoom#bin#Zoom.exe`, start: 7}},
	}}
	procs := &fakeProcs{
		titles_: map[string][]string{
			"discord.exe": nil, "zoom.exe": nil, "obs64.exe": nil,
			"firefox.exe":          {"Meet - abc-defg-hij — Mozilla Firefox"},
			"nvidia broadcast.exe": nil,
		},
		paths: map[string]string{"zoom.exe": `C:\Program Files\Zoom\bin\Zoom.exe`},
		pids: map[uint32]string{
			10: "zoom.exe", 11: "zoom.exe", 20: "firefox.exe", 30: "nvidia broadcast.exe",
			40: "obs64.exe", 50: "discord.exe", 99: "knockblock.exe",
		},
	}
	audio := &fakeAudio{sessions: []fakeSession{
		{10, true, false}, {11, true, true}, // two sessions of one app count once; one unmuted is enough
		{20, true, true},   // muted
		{30, true, false},  // built-in ignore: records for other apps
		{40, true, false},  // ignored by the user
		{50, false, false}, // Discord open, not in a call
		{99, true, false},  // this process
		{77, true, false},  // exited meanwhile
	}}
	d := newConsentStore(w, procs, Options{Ignore: []string{"OBS64.exe"}}).withAudio(audio, 99)
	var s Snapshot
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Mic), []string{"Meet (Firefox)", "Zoom"}; !slices.Equal(got, want) {
		t.Errorf("mic = %v, want %v", got, want)
	}
	if got := names(s.Camera); !slices.Equal(got, []string{"Zoom"}) {
		t.Errorf("camera = %v", got)
	}
	for _, a := range s.Mic {
		if a.Name == "Zoom" && a.Key != `C:#Program Files#Zoom#bin#Zoom.exe` {
			t.Errorf("Zoom key %q, want the consent store form", a.Key)
		}
		if want := a.Name != "Zoom"; a.Muted != want {
			t.Errorf("%s muted = %v, want %v", a.Name, a.Muted, want)
		}
	}

	// Nobody records, though Discord is open.
	audio.sessions = []fakeSession{{50, false, false}}
	if err := d.Poll(&s); err != nil || len(s.Mic) != 0 {
		t.Errorf("idle: mic %v, err %v", names(s.Mic), err)
	}

	// Audio sessions unavailable: the consent store decides.
	audio.err = errors.New("no audio service")
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Mic), []string{"Discord", "Teams"}; !slices.Equal(got, want) {
		t.Errorf("fallback mic = %v, want %v", got, want)
	}
}

// fakeCamera serves fixed camera streams.
type fakeCamera struct {
	pids []uint32
	err  error
}

func (c *fakeCamera) streams(fn func(uint32)) error {
	for _, pid := range c.pids {
		fn(pid)
	}
	return c.err
}

func (c *fakeCamera) close() error { return nil }

func TestCameraStreams(t *testing.T) {
	// The consent store says Zoom uses the camera: stale, the camera
	// streams are what counts.
	w := &fakeWalker{entries: map[Device][]fakeEntry{
		Camera: {{key: `C:#Program Files#Zoom#bin#Zoom.exe`, start: 7}},
	}}
	procs := &fakeProcs{
		titles_: map[string][]string{
			"zoom.exe": nil, "discord.exe": nil, "obs64.exe": nil,
			"chrome.exe":           {"Meet - abc-defg-hij - Google Chrome"},
			"nvidia broadcast.exe": nil,
		},
		pids: map[uint32]string{
			10: "zoom.exe", 20: "chrome.exe", 30: "nvidia broadcast.exe",
			40: "obs64.exe", 50: "discord.exe", 99: "knockblock.exe",
		},
	}
	audio := &fakeAudio{}
	cam := &fakeCamera{pids: []uint32{20, 20, 40, 99, 77}}
	d := newConsentStore(w, procs, Options{Ignore: []string{"obs64.exe"}}).withAudio(audio, 99).withCamera(cam, 99)
	var s Snapshot
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Camera), []string{"Meet (Chrome)"}; !slices.Equal(got, want) {
		t.Errorf("camera = %v, want %v", got, want)
	}

	// NVIDIA Broadcast streams the camera for whoever reads its virtual
	// camera: the app in a call, Discord here.
	cam.pids = []uint32{30}
	audio.sessions = []fakeSession{{50, true, true}}
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Camera), []string{"Discord"}; !slices.Equal(got, want) {
		t.Errorf("relayed camera = %v, want %v", got, want)
	}
	if !s.Mic[0].Muted || s.Camera[0].Muted {
		t.Errorf("muted flags: mic %v, camera %v", s.Mic[0].Muted, s.Camera[0].Muted)
	}

	// Nobody records: the relay itself is shown.
	audio.sessions = nil
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Camera), []string{"NVIDIA Broadcast"}; !slices.Equal(got, want) {
		t.Errorf("relay alone = %v, want %v", got, want)
	}

	// Camera streams unavailable: the consent store decides.
	cam.err = errors.New("no camera service")
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if got, want := names(s.Camera), []string{"Zoom"}; !slices.Equal(got, want) {
		t.Errorf("fallback camera = %v, want %v", got, want)
	}
}

func TestWebAppFromTitle(t *testing.T) {
	tests := map[string]string{
		"Chat | Microsoft Teams — Mozilla Firefox": "Teams",
		"Meet - abc-defg-hij - Google Chrome":      "Meet",
		"Zoom Meeting - Microsoft Edge":            "Zoom",
		"#general | My Server - Discord - Brave":   "Discord",
		"Inbox (3) - me@example.com - Gmail":       "",
		"":                                         "",
	}
	for title, want := range tests {
		if got := WebAppFromTitle(title); got != want {
			t.Errorf("%q: %q, want %q", title, got, want)
		}
	}
}
