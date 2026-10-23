package detect

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// appMutes tells whether an app is muted inside the app itself, which
// Windows cannot see: Discord keeps recording while muted.
type appMutes interface {
	// muted reports whether the app with this lower-case executable name
	// is muted, and whether that is known.
	muted(exe string) (muted, known bool)
}

// discordMutes reads Discord's own mute state from its local storage.
//
// Discord keeps its voice settings, the mute and deafen buttons included,
// in the "MediaEngineStore" entry of its localStorage, which Chromium stores
// as a LevelDB database under %APPDATA%\discord\Local Storage\leveldb. The
// value is JSON: {"default":{...,"mute":true,"deaf":false,...},...}.
// LevelDB appends every write to the current .log file, so the last
// occurrence there is the newest; after a compaction the entry may only be
// in a .ldb table (readable when its block is not compressed).
//
// This is undocumented and may break with a Discord update; then the mute
// is unknown and the microphone counts as on (docs/detection-notes.md).
// Files are read only when they change, in fixed-size chunks.
type discordMutes struct {
	appData string // %APPDATA%
	now     func() time.Time
	cache   map[string]*discordState // by Discord flavour directory
	buf     []byte
}

type discordState struct {
	checked time.Time // last look at the files
	file    string    // file the value came from
	size    int64
	mod     time.Time
	muted   bool
	known   bool
}

// discordDirs maps Discord's executables to their data directories.
var discordDirs = map[string]string{
	"discord.exe":       "discord",
	"discordptb.exe":    "discordptb",
	"discordcanary.exe": "discordcanary",
}

// discordRecheck limits how often the files are listed; a poll in between
// reuses the last answer.
const discordRecheck = time.Second

var discordKey = []byte("MediaEngineStore")

// discordChunk is the read size; an entry is far smaller than the overlap
// kept between chunks.
const discordChunk, discordTail = 64 << 10, 8 << 10

func newDiscordMutes(appData string) *discordMutes {
	return &discordMutes{appData: appData, now: time.Now, cache: map[string]*discordState{}}
}

func (d *discordMutes) muted(exe string) (bool, bool) {
	dir, ok := discordDirs[exe]
	if !ok || d.appData == "" {
		return false, false
	}
	st := d.cache[dir]
	if st == nil {
		st = &discordState{}
		d.cache[dir] = st
	}
	now := d.now()
	if now.Sub(st.checked) < discordRecheck {
		return st.muted, st.known
	}
	st.checked = now
	d.refresh(filepath.Join(d.appData, dir, "Local Storage", "leveldb"), st)
	return st.muted, st.known
}

// refresh reads the newest MediaEngineStore value if its file changed.
func (d *discordMutes) refresh(dbDir string, st *discordState) {
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		st.known = false
		return
	}
	// Newest first: the .log files, then the tables, by file number
	// (LevelDB names files with increasing numbers).
	var logs, tables []string
	for _, e := range entries {
		switch name := e.Name(); {
		case strings.HasSuffix(name, ".log"):
			logs = append(logs, name)
		case strings.HasSuffix(name, ".ldb"):
			tables = append(tables, name)
		}
	}
	slices.Sort(logs)
	slices.Sort(tables)
	slices.Reverse(logs)
	slices.Reverse(tables)
	for _, name := range append(logs, tables...) {
		path := filepath.Join(dbDir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if path == st.file && info.Size() == st.size && info.ModTime().Equal(st.mod) {
			return // unchanged: the cached value stands
		}
		// A .log file only grows: read just what was appended since the
		// value was found (with some overlap). Without a newer value there
		// the cached one stands.
		var from int64
		if path == st.file && strings.HasSuffix(path, ".log") && info.Size() > st.size {
			from = max(0, st.size-discordTail)
		}
		muted, ok := d.scan(path, from)
		if !ok && from > 0 {
			st.size, st.mod = info.Size(), info.ModTime()
			return
		}
		if !ok {
			continue
		}
		st.file, st.size, st.mod = path, info.Size(), info.ModTime()
		st.muted, st.known = muted, true
		return
	}
	st.known = false
}

// scan returns the mute state from the last MediaEngineStore value in a
// file, read from offset from.
func (d *discordMutes) scan(path string, from int64) (muted, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	if from > 0 {
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return false, false
		}
	}
	if cap(d.buf) < discordChunk+discordTail {
		d.buf = make([]byte, 0, discordChunk+discordTail)
	}
	buf := d.buf[:0]
	for {
		n, err := io.ReadFull(f, buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		// The last complete value in this window wins over earlier ones.
		for i := 0; ; {
			j := bytes.Index(buf[i:], discordKey)
			if j < 0 {
				break
			}
			i += j + len(discordKey)
			if m, deaf, found := parseMediaEngine(buf[i:]); found {
				muted, ok = m || deaf, true
			}
		}
		if err != nil { // io.EOF or io.ErrUnexpectedEOF: done
			return muted, ok
		}
		// Keep the tail: a value may cross the chunk boundary.
		keep := min(discordTail, len(buf))
		copy(buf, buf[len(buf)-keep:])
		buf = buf[:keep]
	}
}

// parseMediaEngine reads "mute" and "deaf" of the "default" context from the
// bytes after the key: a length, a one-byte encoding marker, then the JSON.
func parseMediaEngine(b []byte) (mute, deaf, found bool) {
	start := bytes.Index(b[:min(len(b), 16)], []byte(`{"default":{`))
	if start < 0 {
		return false, false, false
	}
	b = b[start+len(`{"default":{`):]
	// The "default" object ends at its closing brace; settings of other
	// contexts come after it.
	depth, end := 1, -1
	for i := 0; i < len(b) && end < 0; i++ {
		switch b[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return false, false, false // cut off
	}
	b = b[:end]
	mute, okMute := jsonBool(b, `"mute":`)
	deaf, okDeaf := jsonBool(b, `"deaf":`)
	return mute, deaf, okMute && okDeaf
}

// jsonBool finds `name` followed by true or false in a flat JSON object.
func jsonBool(b []byte, name string) (value, ok bool) {
	i := bytes.Index(b, []byte(name))
	if i < 0 {
		return false, false
	}
	rest := b[i+len(name):]
	switch {
	case bytes.HasPrefix(rest, []byte("true")):
		return true, true
	case bytes.HasPrefix(rest, []byte("false")):
		return false, true
	}
	return false, false
}
