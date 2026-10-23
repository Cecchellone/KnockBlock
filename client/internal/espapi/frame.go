package espapi

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// transport reads and writes whole API messages (type + protobuf payload)
// over a connection, hiding the plaintext or Noise framing.
//
// readFrame returns a payload that stays valid only until the next call.
// readFrame must be called from a single goroutine; writeFrame calls must be
// serialized by the caller.
type transport interface {
	readFrame() (msgType uint32, payload []byte, err error)
	writeFrame(msgType uint32, payload []byte) error
}

const (
	// maxFrameSize bounds a frame payload. It matches the firmware's uint16
	// wire limit (aioesphomeapi/_frame_helper/plain_text.py,
	// MAX_PLAINTEXT_FRAME_SIZE).
	maxFrameSize = 65535
	// maxVarintBytes caps varints in the plaintext header, as aioesphomeapi
	// does (_MAX_VARUINT_BYTES), so a peer cannot make us read forever.
	maxVarintBytes = 4

	preamblePlaintext = 0x00
	preambleNoise     = 0x01
)

// plaintextTransport implements the plaintext framing: a zero byte, the
// payload length as a varint, the message type as a varint, the payload
// (api.proto, "BASE PACKETS"; aioesphomeapi/_frame_helper/plain_text.py).
type plaintextTransport struct {
	r    *bufio.Reader
	w    io.Writer
	rbuf []byte // reused for payloads
	wbuf []byte // reused for outgoing frames
}

func newPlaintextTransport(r *bufio.Reader, w io.Writer) *plaintextTransport {
	return &plaintextTransport{r: r, w: w}
}

func (t *plaintextTransport) readFrame() (uint32, []byte, error) {
	preamble, err := t.r.ReadByte()
	if err != nil {
		return 0, nil, readError(err)
	}
	switch preamble {
	case preamblePlaintext:
	case preambleNoise:
		return 0, nil, ErrRequiresEncryption
	default:
		return 0, nil, fmt.Errorf("%w: invalid preamble 0x%02x", ErrProtocol, preamble)
	}
	length, err := readVarint(t.r)
	if err != nil {
		return 0, nil, err
	}
	if length > maxFrameSize {
		return 0, nil, fmt.Errorf("%w: frame length %d exceeds %d", ErrProtocol, length, maxFrameSize)
	}
	msgType, err := readVarint(t.r)
	if err != nil {
		return 0, nil, err
	}
	if cap(t.rbuf) < int(length) {
		t.rbuf = make([]byte, length)
	}
	payload := t.rbuf[:length]
	if _, err := io.ReadFull(t.r, payload); err != nil {
		return 0, nil, readError(err)
	}
	return msgType, payload, nil
}

func (t *plaintextTransport) writeFrame(msgType uint32, payload []byte) error {
	if len(payload) > maxFrameSize {
		return fmt.Errorf("espapi: message of %d bytes exceeds %d", len(payload), maxFrameSize)
	}
	t.wbuf = appendPlaintextFrame(t.wbuf[:0], msgType, payload)
	_, err := t.w.Write(t.wbuf)
	return err
}

// appendPlaintextFrame appends one plaintext frame to dst.
func appendPlaintextFrame(dst []byte, msgType uint32, payload []byte) []byte {
	dst = append(dst, preamblePlaintext)
	dst = appendVarint(dst, uint32(len(payload)))
	dst = appendVarint(dst, msgType)
	return append(dst, payload...)
}

func appendVarint(dst []byte, v uint32) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func readVarint(r io.ByteReader) (uint32, error) {
	var v uint32
	for i := 0; i < maxVarintBytes; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, readError(err)
		}
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%w: varint exceeds %d bytes", ErrProtocol, maxVarintBytes)
}

// readError maps end-of-stream and connection resets to ErrClosed and keeps
// other errors.
func readError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isConnReset(err) {
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return err
}
