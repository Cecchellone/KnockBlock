package detect

import (
	"bytes"
	"encoding/binary"
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
//
// Every write goes to LevelDB's current .log file, so pressing mute or
// unmute always appends a new value there. The log is read as LevelDB
// writes it (32 KiB blocks of records holding write batches), only the part
// appended since the last read. Discord writes a lot, and LevelDB starts a
// new log every few minutes, moving older values into .ldb tables
// (Snappy-compressed). When the current log holds no value, the newest one
// is taken from the tables by its sequence number (leveldb.go): an older
// table can still hold an outdated value, which made the light stick on a
// stale mute state when tables were searched as plain text.
//
// This is undocumented and may break with a Discord update; then the mute
// is unknown and the microphone counts as on (docs/detection-notes.md).
type discordMutes struct {
	appData string // %APPDATA%
	now     func() time.Time
	cache   map[string]*discordState // by Discord flavour directory
	block   [logBlockSize]byte
	record  []byte // a record being reassembled from fragments, reused
}

type discordState struct {
	checked time.Time // last look at the files
	file    string    // log read so far
	offset  int64     // where the next record starts in file
	inLog   bool      // file held a value
	tables  bool      // the tables were read for file
	muted   bool
	known   bool
}

// discordDirs maps Discord's executables to their data directories.
var discordDirs = map[string]string{
	"discord.exe":       "discord",
	"discordptb.exe":    "discordptb",
	"discordcanary.exe": "discordcanary",
}

// discordRecheck limits how often the log is looked at; a poll in between
// reuses the last answer.
const discordRecheck = time.Second

// discordKey is the localStorage key as Chromium writes it: origin, a NUL,
// the 8-bit encoding marker, the key name.
var discordKey = []byte("_https://discord.com\x00\x01MediaEngineStore")

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

// refresh reads what was appended to the current log since the last look.
func (d *discordMutes) refresh(dbDir string, st *discordState) {
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		return // keep the last value
	}
	var logs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) == 0 {
		return
	}
	// LevelDB numbers its files in increasing order: the highest is the
	// current log.
	path := filepath.Join(dbDir, slices.Max(logs))
	if path != st.file {
		st.file, st.offset, st.inLog, st.tables = path, 0, false, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < st.offset {
		st.offset, st.inLog, st.tables = 0, false, false // rewritten: start over
	}
	if info.Size() > st.offset {
		st.offset = d.readLog(path, st.offset, st)
	}
	if !st.inLog && !st.tables {
		st.tables = true
		d.fromTables(dbDir, st)
	}
}

// fromTables takes the newest value from the .ldb tables, once per log
// that has none yet.
func (d *discordMutes) fromTables(dbDir string, st *discordState) {
	tables, _ := filepath.Glob(filepath.Join(dbDir, "*.ldb"))
	var best []byte
	var bestSeq uint64
	for _, t := range tables {
		v, seq, found, err := tableLookup(t, discordKey)
		if err == nil && found && (best == nil || seq > bestSeq) {
			best, bestSeq = v, seq
		}
	}
	if best == nil {
		return
	}
	if mute, deaf, ok := parseMediaEngine(best); ok {
		st.muted, st.known = mute || deaf, true
	}
}

// LevelDB log format (leveldb/doc/log_format.md): 32 KiB blocks; each
// record has a 7-byte header (CRC32C, length, type) and may be split into
// FIRST, MIDDLE and LAST fragments across blocks. A block's last 6 bytes or
// fewer are padding.
const (
	logBlockSize  = 32 << 10
	logHeaderSize = 7

	recFull   = 1
	recFirst  = 2
	recMiddle = 3
	recLast   = 4
)

// readLog reads the records from offset (a record boundary) to the end and
// returns where the next unread record starts. Each complete record is a
// write batch, whose MediaEngineStore values update st in order.
func (d *discordMutes) readLog(path string, offset int64, st *discordState) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	blockStart := offset - offset%logBlockSize
	pos := int(offset - blockStart) // within the block
	next := offset                  // after the last complete record
	d.record = d.record[:0]
	inRecord := false
	for {
		n, err := f.ReadAt(d.block[:], blockStart)
		if n == 0 {
			return next
		}
		b := d.block[:n]
		for pos+logHeaderSize <= len(b) {
			length := int(binary.LittleEndian.Uint16(b[pos+4:]))
			typ := b[pos+6]
			if typ == 0 && length == 0 {
				break // zero padding: the rest of the block is unused
			}
			end := pos + logHeaderSize + length
			if end > len(b) {
				return next // a record still being written
			}
			data := b[pos+logHeaderSize : end]
			pos = end
			switch typ {
			case recFull:
				d.batch(data, st)
				inRecord = false
				next = blockStart + int64(pos)
			case recFirst:
				d.record = append(d.record[:0], data...)
				inRecord = true
			case recMiddle:
				if inRecord {
					d.record = append(d.record, data...)
				}
			case recLast:
				if inRecord {
					d.batch(append(d.record, data...), st)
				}
				inRecord = false
				next = blockStart + int64(pos)
			}
		}
		if n < logBlockSize || err == io.EOF {
			return next
		}
		blockStart += logBlockSize
		pos = 0
	}
}

// batch applies the MediaEngineStore values of a LevelDB write batch:
// sequence (8 bytes), count (4), then for each entry a tag (1 = put,
// 0 = delete), the key and, for a put, the value, both length-prefixed with
// a varint.
func (d *discordMutes) batch(b []byte, st *discordState) {
	if len(b) < 12 {
		return
	}
	b = b[12:]
	for len(b) > 0 {
		tag := b[0]
		b = b[1:]
		key, rest, ok := lengthPrefixed(b)
		if !ok {
			return
		}
		b = rest
		if tag != 1 {
			continue // a delete has no value
		}
		value, rest, ok := lengthPrefixed(b)
		if !ok {
			return
		}
		b = rest
		if !bytes.Equal(key, discordKey) {
			continue
		}
		if mute, deaf, found := parseMediaEngine(value); found {
			st.muted, st.known, st.inLog = mute || deaf, true, true
		}
	}
}

func lengthPrefixed(b []byte) (field, rest []byte, ok bool) {
	n, k := binary.Uvarint(b)
	if k <= 0 || uint64(len(b)-k) < n {
		return nil, nil, false
	}
	return b[k : k+int(n)], b[k+int(n):], true
}

// parseMediaEngine reads "mute" and "deaf" of the "default" context from a
// MediaEngineStore value: an encoding marker (1: 8-bit, 0: UTF-16LE), then
// the JSON.
func parseMediaEngine(v []byte) (mute, deaf, found bool) {
	if len(v) == 0 {
		return false, false, false
	}
	b := v[1:]
	if v[0] == 0 {
		// UTF-16LE: the JSON syntax and the names searched are ASCII, so
		// the low bytes are enough.
		ascii := make([]byte, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if b[i+1] == 0 {
				ascii = append(ascii, b[i])
			} else {
				ascii = append(ascii, '?')
			}
		}
		b = ascii
	}
	const open = `{"default":{`
	if !bytes.HasPrefix(b, []byte(open)) {
		return false, false, false
	}
	b = b[len(open):]
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

// jsonBool finds `name` followed by true or false in a JSON object.
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
