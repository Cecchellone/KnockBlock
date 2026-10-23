package espapi

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/flynn/noise"
)

// Noise transport (aioesphomeapi/_frame_helper/noise.py and
// _frame_helper/noise_encryption.py).
//
// Every frame is 0x01, a big-endian uint16 length and the frame data.
// Handshake, client side:
//
//	client → 01 00 00                  empty ClientHello frame
//	client → 01 len | 00 | message 1   Noise handshake message (initiator)
//	device → 01 len | 01 | name 00 | mac 00     ServerHello (chosen protocol 0x01)
//	device → 01 len | 00 | message 2   handshake response
//	         01 len | 01 | reason      or: rejected ("Handshake MAC failure" = wrong key)
//
// After the handshake the frame data is encrypted; the plaintext is a
// uint16 message type, a uint16 payload length and the payload.

// noisePrologue is "NoiseAPIInit" followed by the 2-byte length of the empty
// ClientHello frame (the device's api_frame_helper_noise.cpp builds it the
// same way; aioesphomeapi hard-codes it).
var noisePrologue = []byte("NoiseAPIInit\x00\x00")

var noiseSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

const (
	// noiseProtocolNNpsk0 is the only protocol the device offers in
	// ServerHello: Noise_NNpsk0_25519_ChaChaPoly_SHA256.
	noiseProtocolNNpsk0 = 0x01
	// noiseTagSize is the ChaChaPoly authentication tag size.
	noiseTagSize = 16
	// noiseInnerHeader is the message type and length before the payload.
	noiseInnerHeader = 4
	// invalidKeyReason is the device's rejection text for a wrong PSK.
	invalidKeyReason = "Handshake MAC failure"
)

// decodeNoiseKey decodes the base64 PSK from the device config into its 32
// raw bytes (noise_encryption.py, decode_noise_psk).
func decodeNoiseKey(key string) ([]byte, error) {
	psk, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("%w: not valid base64", ErrInvalidKeyFormat)
	}
	if len(psk) != 32 {
		return nil, fmt.Errorf("%w: %d bytes, expected 32", ErrInvalidKeyFormat, len(psk))
	}
	return psk, nil
}

// serverHello is what the device announces before the handshake.
type serverHello struct {
	name string
	mac  string
}

// noiseClientHandshake runs the client side of the handshake and returns
// the encrypted transport. rng supplies the ephemeral key; nil means
// crypto/rand (tests pass a fixed key to replay recorded vectors).
func noiseClientHandshake(r *bufio.Reader, w io.Writer, psk []byte, expectedName string, rng io.Reader) (*noiseTransport, serverHello, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noiseSuite,
		Random:                rng,
		Pattern:               noise.HandshakeNN,
		Initiator:             true,
		Prologue:              noisePrologue,
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, serverHello{}, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	msg1, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, serverHello{}, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	// One write, as aioesphomeapi does (_send_hello_handshake).
	out := appendNoiseFrame([]byte{preambleNoise, 0x00, 0x00}, append([]byte{0x00}, msg1...))
	if _, err := w.Write(out); err != nil {
		return nil, serverHello{}, err
	}

	var buf []byte
	data, err := readNoiseFrame(r, &buf)
	if err != nil {
		return nil, serverHello{}, helloError(err)
	}
	hello, err := parseServerHello(data)
	if err != nil {
		return nil, serverHello{}, err
	}
	if expectedName != "" && hello.name != expectedName {
		return nil, hello, fmt.Errorf("%w: expected %q, device is %q", ErrBadName, expectedName, hello.name)
	}

	data, err = readNoiseFrame(r, &buf)
	if err != nil {
		return nil, hello, err
	}
	if len(data) == 0 {
		return nil, hello, fmt.Errorf("%w: empty handshake frame", ErrHandshake)
	}
	if data[0] != 0x00 {
		reason := string(data[1:])
		if reason == invalidKeyReason {
			return nil, hello, ErrInvalidKey
		}
		return nil, hello, fmt.Errorf("%w: rejected by the device: %q", ErrHandshake, reason)
	}
	_, enc, dec, err := hs.ReadMessage(nil, data[1:])
	if err != nil {
		// The response does not authenticate: the PSK differs (noise.py,
		// _handle_handshake, InvalidTag).
		return nil, hello, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	if enc == nil || dec == nil {
		return nil, hello, fmt.Errorf("%w: handshake did not complete", ErrHandshake)
	}
	return newNoiseTransport(r, w, enc, dec), hello, nil
}

// helloError explains a connection that drops right after our hello: the
// device most likely has no encryption (noise.py, _handle_error).
func helloError(err error) error {
	if errors.Is(err, ErrClosed) {
		return fmt.Errorf("%w (the device may not use encryption): %v", ErrClosed, err)
	}
	return err
}

func parseServerHello(data []byte) (serverHello, error) {
	if len(data) == 0 {
		return serverHello{}, fmt.Errorf("%w: empty ServerHello", ErrHandshake)
	}
	if data[0] != noiseProtocolNNpsk0 {
		return serverHello{}, fmt.Errorf("%w: device chose unknown protocol 0x%02x", ErrHandshake, data[0])
	}
	var h serverHello
	// Name (since ESPHome 2022.2) and MAC (since 2025.4), each followed by 0.
	rest := data[1:]
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		h.name = string(rest[:i])
		rest = rest[i+1:]
		if j := bytes.IndexByte(rest, 0); j >= 0 {
			h.mac = string(rest[:j])
		}
	}
	return h, nil
}

// readNoiseFrame reads one frame and returns its data, valid until the next
// call with the same buffer.
func readNoiseFrame(r *bufio.Reader, buf *[]byte) ([]byte, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, readError(err)
	}
	switch hdr[0] {
	case preambleNoise:
	case preamblePlaintext:
		return nil, ErrDevicePlaintext
	default:
		return nil, fmt.Errorf("%w: invalid preamble 0x%02x", ErrProtocol, hdr[0])
	}
	n := int(hdr[1])<<8 | int(hdr[2])
	if cap(*buf) < n {
		*buf = make([]byte, n)
	}
	data := (*buf)[:n]
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, readError(err)
	}
	return data, nil
}

// appendNoiseFrame appends a frame header and data to dst.
func appendNoiseFrame(dst, data []byte) []byte {
	dst = append(dst, preambleNoise, byte(len(data)>>8), byte(len(data)))
	return append(dst, data...)
}

// noiseTransport encrypts and decrypts frames after the handshake.
type noiseTransport struct {
	r    *bufio.Reader
	w    io.Writer
	enc  *noise.CipherState
	dec  *noise.CipherState
	rbuf []byte // ciphertext in
	pbuf []byte // plaintext in
	ibuf []byte // plaintext out
	wbuf []byte // frame out
}

func newNoiseTransport(r *bufio.Reader, w io.Writer, enc, dec *noise.CipherState) *noiseTransport {
	return &noiseTransport{r: r, w: w, enc: enc, dec: dec}
}

func (t *noiseTransport) readFrame() (uint32, []byte, error) {
	ct, err := readNoiseFrame(t.r, &t.rbuf)
	if err != nil {
		return 0, nil, err
	}
	pt, err := t.dec.Decrypt(t.pbuf[:0], nil, ct)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: decryption failed: %v", ErrProtocol, err)
	}
	t.pbuf = pt
	// Bound-check before slicing; the inner length is not trusted
	// (noise.py, _handle_frame).
	if len(pt) < noiseInnerHeader {
		return 0, nil, fmt.Errorf("%w: decrypted frame of %d bytes", ErrProtocol, len(pt))
	}
	msgType := uint32(pt[0])<<8 | uint32(pt[1])
	return msgType, pt[noiseInnerHeader:], nil
}

func (t *noiseTransport) writeFrame(msgType uint32, payload []byte) error {
	if msgType > 0xffff {
		return fmt.Errorf("espapi: message type %d does not fit in a Noise frame", msgType)
	}
	if noiseInnerHeader+len(payload)+noiseTagSize > maxFrameSize {
		return fmt.Errorf("espapi: message of %d bytes exceeds the Noise frame size", len(payload))
	}
	t.ibuf = append(t.ibuf[:0], byte(msgType>>8), byte(msgType), byte(len(payload)>>8), byte(len(payload)))
	t.ibuf = append(t.ibuf, payload...)
	frame := append(t.wbuf[:0], preambleNoise, 0, 0)
	frame, err := t.enc.Encrypt(frame, nil, t.ibuf)
	if err != nil {
		return fmt.Errorf("espapi: encrypt: %w", err)
	}
	n := len(frame) - 3
	frame[1], frame[2] = byte(n>>8), byte(n)
	t.wbuf = frame
	_, err = t.w.Write(frame)
	return err
}

// ValidateKey checks that key is a valid API encryption key: base64 of 32
// bytes, as in the firmware's `api: encryption: key:`.
func ValidateKey(key string) error {
	_, err := decodeNoiseKey(key)
	return err
}
