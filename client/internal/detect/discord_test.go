package detect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mediaEngine is a MediaEngineStore entry as Chromium writes it in a
// LevelDB file: key, value length, encoding marker, JSON.
func mediaEngine(mute, deaf bool) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return "\x01&_https://discord.com\x00\x01MediaEngineStore\xfd(\x01" +
		`{"default":{"mode":"VOICE_ACTIVITY","modeOptions":{"threshold":-60,"shortcut":[]},` +
		`"mute":` + b(mute) + `,"deaf":` + b(deaf) + `,"echoCancellation":true},` +
		`"stream":{"mute":false,"deaf":false}}`
}

func TestDiscordMutes(t *testing.T) {
	appData := t.TempDir()
	db := filepath.Join(appData, "discord", "Local Storage", "leveldb")
	if err := os.MkdirAll(db, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(db, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clock := time.Unix(1000, 0)
	d := newDiscordMutes(appData)
	d.now = func() time.Time { clock = clock.Add(2 * time.Second); return clock }

	if _, known := d.muted("discord.exe"); known {
		t.Fatal("known with no entry")
	}

	// Only in a table: an older value.
	write("000010.ldb", "junk"+mediaEngine(false, false)+"junk")
	if m, known := d.muted("discord.exe"); !known || m {
		t.Fatalf("table: muted %v, known %v", m, known)
	}

	// The log is newer, and its last write wins.
	write("000012.log", mediaEngine(false, false)+"other keys"+mediaEngine(true, false))
	if m, known := d.muted("discord.exe"); !known || !m {
		t.Fatalf("log: muted %v, known %v", m, known)
	}

	// Appended writes: unmuted, then deafened (deafened means muted too).
	f, err := os.OpenFile(filepath.Join(db, "000012.log"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(mediaEngine(false, false))
	if m, _ := d.muted("discord.exe"); m {
		t.Fatal("still muted after unmuting")
	}
	f.WriteString(mediaEngine(false, true))
	f.Close()
	if m, _ := d.muted("discord.exe"); !m {
		t.Fatal("deafened but not muted")
	}

	// Writes of other keys keep the value.
	f, _ = os.OpenFile(filepath.Join(db, "000012.log"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("unrelated write")
	f.Close()
	if m, known := d.muted("discord.exe"); !known || !m {
		t.Fatalf("after other writes: muted %v, known %v", m, known)
	}

	// Other apps are not Discord.
	if _, known := d.muted("teams.exe"); known {
		t.Fatal("teams known")
	}
}

func TestParseMediaEngineCutOff(t *testing.T) {
	v := mediaEngine(true, false)
	prefix := len("\x01&_https://discord.com\x00\x01MediaEngineStore")
	cut := v[prefix : len(v)-60] // the "default" object does not end
	if _, _, found := parseMediaEngine([]byte(cut)); found {
		t.Fatal("found in a cut-off value")
	}
}

func TestDiscordAppMuteCounts(t *testing.T) {
	w := &fakeWalker{}
	procs := &fakeProcs{pids: map[uint32]string{50: "discord.exe"}, titles_: map[string][]string{"discord.exe": nil}}
	audio := &fakeAudio{sessions: []fakeSession{{50, true, false}}}
	d := newConsentStore(w, procs, Options{}).withAudio(audio, 99).withAppMutes(fixedMutes{"discord.exe": true})
	var s Snapshot
	if err := d.Poll(&s); err != nil {
		t.Fatal(err)
	}
	if len(s.Mic) != 1 || !s.Mic[0].Muted {
		t.Fatalf("mic %+v, want Discord muted", s.Mic)
	}
}

type fixedMutes map[string]bool

func (f fixedMutes) muted(exe string) (bool, bool) {
	m, ok := f[exe]
	return m, ok
}

// TestDiscordMutesManual reads this PC's Discord, with
// KNOCKBLOCK_DISCORD_TEST=1.
func TestDiscordMutesManual(t *testing.T) {
	if os.Getenv("KNOCKBLOCK_DISCORD_TEST") != "1" {
		t.Skip("set KNOCKBLOCK_DISCORD_TEST=1 to read this PC's Discord")
	}
	appData, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	m, known := newDiscordMutes(appData).muted("discord.exe")
	t.Logf("Discord muted %v, known %v", m, known)
}
