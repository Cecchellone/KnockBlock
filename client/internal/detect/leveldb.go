package detect

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
)

// A minimal reader for LevelDB tables (.ldb), enough to find the newest
// value of one key: footer, index block, data blocks (Snappy-compressed or
// not). Format: leveldb/doc/table_format.md.

const (
	tableMagic      = 0xdb4775248b80fb57
	tableFooterSize = 48
	blockTrailer    = 5 // compression type, CRC32C
	noCompression   = 0
	snappyBlock     = 1
	maxTableBlock   = 4 << 20 // larger blocks are not expected; skip them
)

var errBadTable = errors.New("detect: unreadable LevelDB table")

// tableLookup returns the value of key with the highest sequence number in
// the table at path, and that sequence number. found is false when the
// table does not hold the key (or holds only a deletion).
func tableLookup(path string, key []byte) (value []byte, seq uint64, found bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if info.Size() < tableFooterSize {
		return nil, 0, false, errBadTable
	}
	footer := make([]byte, tableFooterSize)
	if _, err := f.ReadAt(footer, info.Size()-tableFooterSize); err != nil {
		return nil, 0, false, err
	}
	if binary.LittleEndian.Uint64(footer[40:]) != tableMagic {
		return nil, 0, false, errBadTable
	}
	// The metaindex handle comes first; the index handle follows it.
	_, n1 := binary.Uvarint(footer)
	_, n2 := binary.Uvarint(footer[max(n1, 0):])
	if n1 <= 0 || n2 <= 0 {
		return nil, 0, false, errBadTable
	}
	indexOff, indexSize, ok := blockHandle(footer[n1+n2:])
	if !ok {
		return nil, 0, false, errBadTable
	}
	index, err := readTableBlock(f, indexOff, indexSize)
	if err != nil {
		return nil, 0, false, err
	}
	// Index entries: the last internal key of each data block (or a key
	// between blocks) → its handle, in key order. Versions of one user
	// key are ordered newest first, and may span two blocks.
	err = blockEntries(index, func(sep, handle []byte) bool {
		if len(sep) < 8 || bytes.Compare(sep[:len(sep)-8], key) < 0 {
			return true // this block ends before the key
		}
		off, size, ok := blockHandle(handle)
		if !ok {
			return false
		}
		data, rerr := readTableBlock(f, off, size)
		if rerr != nil {
			err = rerr
			return false
		}
		more := true
		blockEntries(data, func(ik, v []byte) bool {
			if len(ik) < 8 {
				return true
			}
			switch c := bytes.Compare(ik[:len(ik)-8], key); {
			case c < 0:
				return true
			case c > 0:
				more = false // past the key
				return false
			}
			tag := binary.LittleEndian.Uint64(ik[len(ik)-8:])
			if s := tag >> 8; !found || s > seq {
				seq = s
				if tag&0xff == 1 { // a put; 0 is a deletion
					value, found = append([]byte(nil), v...), true
				} else {
					value, found = nil, false
				}
			}
			return true
		})
		return more
	})
	return value, seq, found, err
}

func blockHandle(b []byte) (off, size uint64, ok bool) {
	off, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, false
	}
	size, m := binary.Uvarint(b[n:])
	return off, size, m > 0
}

// readTableBlock reads a block and its trailer, decompressing it.
func readTableBlock(f *os.File, off, size uint64) ([]byte, error) {
	if size > maxTableBlock {
		return nil, errBadTable
	}
	raw := make([]byte, size+blockTrailer)
	if _, err := f.ReadAt(raw, int64(off)); err != nil {
		return nil, err
	}
	switch raw[size] {
	case noCompression:
		return raw[:size], nil
	case snappyBlock:
		return snappyDecode(raw[:size])
	}
	return nil, errBadTable // zstd or unknown
}

// blockEntries calls fn with each key and value of a block until fn
// returns false. Keys are prefix-compressed: shared length, unshared
// length, value length, then the unshared key bytes and the value; the
// block ends with the restart offsets and their count.
func blockEntries(b []byte, fn func(key, value []byte) bool) error {
	if len(b) < 4 {
		return errBadTable
	}
	restarts := int(binary.LittleEndian.Uint32(b[len(b)-4:]))
	end := len(b) - 4 - 4*restarts
	if end < 0 {
		return errBadTable
	}
	var key []byte
	for pos := 0; pos < end; {
		shared, n1 := binary.Uvarint(b[pos:])
		unshared, n2 := binary.Uvarint(b[pos+max(n1, 0):])
		vlen, n3 := binary.Uvarint(b[pos+max(n1, 0)+max(n2, 0):])
		if n1 <= 0 || n2 <= 0 || n3 <= 0 {
			return errBadTable
		}
		pos += n1 + n2 + n3
		if shared > uint64(len(key)) || uint64(end-pos) < unshared+vlen {
			return errBadTable
		}
		key = append(key[:shared], b[pos:pos+int(unshared)]...)
		pos += int(unshared)
		value := b[pos : pos+int(vlen)]
		pos += int(vlen)
		if !fn(key, value) {
			return nil
		}
	}
	return nil
}

// snappyDecode decodes a Snappy block (format_description.txt of
// google/snappy): the decoded length, then literals and back-references.
func snappyDecode(src []byte) ([]byte, error) {
	n, k := binary.Uvarint(src)
	if k <= 0 || n > maxTableBlock {
		return nil, errBadTable
	}
	dst := make([]byte, 0, n)
	for s := k; s < len(src); {
		tag := src[s]
		var length, offset int
		switch tag & 3 {
		case 0: // literal
			length = int(tag>>2) + 1
			s++
			if length > 60 {
				extra := length - 60 // 1 to 4 bytes of length follow
				if s+extra > len(src) {
					return nil, errBadTable
				}
				length = 0
				for i := 0; i < extra; i++ {
					length |= int(src[s+i]) << (8 * i)
				}
				length++
				s += extra
			}
			if length < 0 || s+length > len(src) {
				return nil, errBadTable
			}
			dst = append(dst, src[s:s+length]...)
			s += length
			continue
		case 1: // copy, 1-byte offset
			if s+2 > len(src) {
				return nil, errBadTable
			}
			length = int(tag>>2&7) + 4
			offset = int(tag&0xe0)<<3 | int(src[s+1])
			s += 2
		case 2: // copy, 2-byte offset
			if s+3 > len(src) {
				return nil, errBadTable
			}
			length = int(tag>>2) + 1
			offset = int(binary.LittleEndian.Uint16(src[s+1:]))
			s += 3
		case 3: // copy, 4-byte offset
			if s+5 > len(src) {
				return nil, errBadTable
			}
			length = int(tag>>2) + 1
			offset = int(binary.LittleEndian.Uint32(src[s+1:]))
			s += 5
		}
		if offset <= 0 || offset > len(dst) || uint64(len(dst)+length) > n {
			return nil, errBadTable
		}
		for i := 0; i < length; i++ { // overlapping copies repeat bytes
			dst = append(dst, dst[len(dst)-offset])
		}
	}
	if uint64(len(dst)) != n {
		return nil, errBadTable
	}
	return dst, nil
}
