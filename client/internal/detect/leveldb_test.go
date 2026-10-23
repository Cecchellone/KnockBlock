package detect

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestSnappyDecode(t *testing.T) {
	// "abcd", then a copy of 4 bytes at offset 4, then an overlapping copy
	// of 6 bytes at offset 2.
	src := []byte{14, 0x0c, 'a', 'b', 'c', 'd', 0x01, 0x04, (6-4)<<2 | 1, 0x02}
	got, err := snappyDecode(src)
	if err != nil || string(got) != "abcdabcdcdcdcd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := snappyDecode([]byte{5, 0x01, 0x09}); err == nil {
		t.Fatal("copy before any data accepted")
	}
}

// block encodes a LevelDB block without prefix compression.
func block(kv ...[]byte) []byte {
	var b []byte
	for i := 0; i+1 < len(kv); i += 2 {
		b = binary.AppendUvarint(b, 0)
		b = binary.AppendUvarint(b, uint64(len(kv[i])))
		b = binary.AppendUvarint(b, uint64(len(kv[i+1])))
		b = append(b, kv[i]...)
		b = append(b, kv[i+1]...)
	}
	b = binary.LittleEndian.AppendUint32(b, 0) // one restart, at 0
	return binary.LittleEndian.AppendUint32(b, 1)
}

func internalKey(user string, seq uint64, put bool) []byte {
	tag := seq << 8
	if put {
		tag |= 1
	}
	return binary.LittleEndian.AppendUint64([]byte(user), tag)
}

// table writes an uncompressed LevelDB table with one data block.
func table(t *testing.T, path string, kv ...[]byte) {
	t.Helper()
	data := block(kv...)
	var f bytes.Buffer
	f.Write(data)
	f.Write([]byte{noCompression, 0, 0, 0, 0})
	var handle []byte
	handle = binary.AppendUvarint(handle, 0)
	handle = binary.AppendUvarint(handle, uint64(len(data)))
	index := block(kv[len(kv)-2], handle)
	indexOff := f.Len()
	f.Write(index)
	f.Write([]byte{noCompression, 0, 0, 0, 0})
	footer := make([]byte, 0, tableFooterSize)
	footer = binary.AppendUvarint(footer, 0) // empty metaindex
	footer = binary.AppendUvarint(footer, 0)
	footer = binary.AppendUvarint(footer, uint64(indexOff))
	footer = binary.AppendUvarint(footer, uint64(len(index)))
	footer = append(footer, make([]byte, 40-len(footer))...)
	footer = binary.LittleEndian.AppendUint64(footer, tableMagic)
	f.Write(footer)
	if err := os.WriteFile(path, f.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTableLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "000001.ldb")
	table(t, path,
		internalKey("a", 1, true), []byte("x"),
		internalKey("key", 9, true), []byte("new"),
		internalKey("key", 5, true), []byte("old"),
		internalKey("z", 2, true), []byte("y"),
	)
	v, seq, found, err := tableLookup(path, []byte("key"))
	if err != nil || !found || seq != 9 || string(v) != "new" {
		t.Fatalf("got %q seq %d found %v err %v", v, seq, found, err)
	}
	if _, _, found, _ := tableLookup(path, []byte("missing")); found {
		t.Fatal("found a missing key")
	}
}
