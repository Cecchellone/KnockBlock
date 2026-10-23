package detect

import (
	"os"
	"path/filepath"
	"slices"
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
	d := newConsentStore(w, Options{
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
