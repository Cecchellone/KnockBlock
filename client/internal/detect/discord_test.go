package detect

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mediaEngineValue is a MediaEngineStore value as Chromium stores it: the
// 8-bit encoding marker, then the JSON.
func mediaEngineValue(mute, deaf bool) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return "\x01" + `{"default":{"mode":"VOICE_ACTIVITY","modeOptions":{"threshold":-60,"shortcut":[]},` +
		`"mute":` + b(mute) + `,"deaf":` + b(deaf) + `,"echoCancellation":true},` +
		`"stream":{"mute":false,"deaf":false}}`
}

// writeBatch encodes a LevelDB write batch of puts (key, value pairs).
func writeBatch(kv ...string) []byte {
	b := make([]byte, 12)
	binary.LittleEndian.PutUint32(b[8:], uint32(len(kv)/2))
	for i := 0; i+1 < len(kv); i += 2 {
		b = append(b, 1)
		b = binary.AppendUvarint(b, uint64(len(kv[i])))
		b = append(b, kv[i]...)
		b = binary.AppendUvarint(b, uint64(len(kv[i+1])))
		b = append(b, kv[i+1]...)
	}
	return b
}

// logWriter writes records in LevelDB's log format, fragmenting them
// across 32 KiB blocks (the CRC is not checked by the reader).
type logWriter struct {
	t    *testing.T
	path string
	off  int // position in the current block
}

func (w *logWriter) add(record []byte) {
	w.t.Helper()
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	first := true
	for {
		if left := logBlockSize - w.off; left < logHeaderSize {
			f.Write(make([]byte, left)) // block trailer
			w.off = 0
		}
		room := logBlockSize - w.off - logHeaderSize
		n := min(room, len(record))
		last := n == len(record)
		typ := byte(recMiddle)
		switch {
		case first && last:
			typ = recFull
		case first:
			typ = recFirst
		case last:
			typ = recLast
		}
		h := make([]byte, logHeaderSize)
		binary.LittleEndian.PutUint16(h[4:], uint16(n))
		h[6] = typ
		f.Write(h)
		f.Write(record[:n])
		w.off += logHeaderSize + n
		record, first = record[n:], false
		if last {
			return
		}
	}
}

const testKey = "_https://discord.com\x00\x01MediaEngineStore"

func TestDiscordMutes(t *testing.T) {
	appData := t.TempDir()
	db := filepath.Join(appData, "discord", "Local Storage", "leveldb")
	if err := os.MkdirAll(db, 0o755); err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1000, 0)
	d := newDiscordMutes(appData)
	d.now = func() time.Time { clock = clock.Add(2 * time.Second); return clock }
	check := func(name string, wantMuted, wantKnown bool) {
		t.Helper()
		if m, known := d.muted("discord.exe"); m != wantMuted || known != wantKnown {
			t.Fatalf("%s: muted %v known %v, want %v %v", name, m, known, wantMuted, wantKnown)
		}
	}

	// An outdated value in an uncompressed table is ignored.
	os.WriteFile(filepath.Join(db, "000010.ldb"), []byte(testKey+"\xfd\x28"+mediaEngineValue(true, false)), 0o644)
	check("table only", false, false)

	w := &logWriter{t: t, path: filepath.Join(db, "000012.log")}
	w.add(writeBatch("_https://discord.com\x00\x01other", "x"))
	check("log without the key", false, false)

	w.add(writeBatch(testKey, mediaEngineValue(true, false)))
	check("muted", true, true)

	// A big batch split across blocks, then unmuted inside it.
	big := strings.Repeat("y", 3*logBlockSize)
	w.add(writeBatch("_https://discord.com\x00\x01big", big, testKey, mediaEngineValue(false, false)))
	check("unmuted in a fragmented batch", false, true)

	w.add(writeBatch(testKey, mediaEngineValue(false, true)))
	check("deafened", true, true)

	// A new log after a compaction, without the key yet: the value stays.
	w2 := &logWriter{t: t, path: filepath.Join(db, "000014.log")}
	w2.add(writeBatch("_https://discord.com\x00\x01other", "z"))
	os.Remove(filepath.Join(db, "000012.log"))
	check("after a new log", true, true)

	w2.add(writeBatch(testKey, mediaEngineValue(false, false)))
	check("unmuted in the new log", false, true)

	// Other apps are not Discord.
	if _, known := d.muted("teams.exe"); known {
		t.Fatal("teams known")
	}
}

func TestParseMediaEngine(t *testing.T) {
	v := mediaEngineValue(true, false)
	if m, deaf, ok := parseMediaEngine([]byte(v)); !ok || !m || deaf {
		t.Fatalf("8-bit: %v %v %v", m, deaf, ok)
	}
	if _, _, ok := parseMediaEngine([]byte(v[:len(v)-60])); ok {
		t.Fatal("found in a cut-off value")
	}
	// UTF-16LE, as Chromium stores values with characters beyond Latin-1.
	u := []byte{0}
	for _, r := range v[1:] {
		u = append(u, byte(r), 0)
	}
	if m, _, ok := parseMediaEngine(u); !ok || !m {
		t.Fatalf("UTF-16: %v %v", m, ok)
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

func TestDiscordMutesFromTables(t *testing.T) {
	appData := t.TempDir()
	db := filepath.Join(appData, "discord", "Local Storage", "leveldb")
	os.MkdirAll(db, 0o755)
	// Two tables: the newer version (higher sequence) wins, whatever the
	// file order.
	table(t, filepath.Join(db, "000020.ldb"), internalKey(testKey, 5, true), []byte(mediaEngineValue(false, false)))
	table(t, filepath.Join(db, "000010.ldb"), internalKey(testKey, 9, true), []byte(mediaEngineValue(true, false)))
	w := &logWriter{t: t, path: filepath.Join(db, "000030.log")}
	w.add(writeBatch("_https://discord.com\x00\x01other", "x"))
	d := newDiscordMutes(appData)
	if m, known := d.muted("discord.exe"); !known || !m {
		t.Fatalf("muted %v known %v, want the newest table value (muted)", m, known)
	}
}
