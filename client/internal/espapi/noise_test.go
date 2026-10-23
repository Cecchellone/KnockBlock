package espapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// noiseVectors is testdata/noise_vectors.json, a complete exchange recorded
// with the Python noise library (testdata/gen_noise_vectors.py).
type noiseVectors struct {
	PSK             string `json:"psk"`
	ClientEphemeral string `json:"client_ephemeral"`
	DeviceName      string `json:"device_name"`
	DeviceMAC       string `json:"device_mac"`
	ClientHandshake string `json:"client_handshake"`
	ServerHello     string `json:"server_hello"`
	ServerHandshake string `json:"server_handshake"`
	HelloRequest    string `json:"hello_request"`
	ClientFrame     string `json:"client_frame"`
	HelloResponse   string `json:"hello_response"`
	ServerFrame     string `json:"server_frame"`
}

func loadNoiseVectors(t *testing.T) noiseVectors {
	t.Helper()
	data, err := os.ReadFile("testdata/noise_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v noiseVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The client replays the recorded exchange byte for byte.
func TestNoiseVectors(t *testing.T) {
	v := loadNoiseVectors(t)
	psk, err := decodeNoiseKey(v.PSK)
	if err != nil {
		t.Fatal(err)
	}
	fromDevice := append(unhex(t, v.ServerHello), unhex(t, v.ServerHandshake)...)
	fromDevice = append(fromDevice, unhex(t, v.ServerFrame)...)
	var toDevice bytes.Buffer

	tr, hello, err := noiseClientHandshake(bufio.NewReader(bytes.NewReader(fromDevice)), &toDevice,
		psk, v.DeviceName, bytes.NewReader(unhex(t, v.ClientEphemeral)))
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if got := hex.EncodeToString(toDevice.Bytes()); got != v.ClientHandshake {
		t.Fatalf("client handshake\n got %s\nwant %s", got, v.ClientHandshake)
	}
	if hello.name != v.DeviceName || hello.mac != v.DeviceMAC {
		t.Fatalf("server hello %+v", hello)
	}

	toDevice.Reset()
	if err := tr.writeFrame(1, unhex(t, v.HelloRequest)); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(toDevice.Bytes()); got != v.ClientFrame {
		t.Fatalf("encrypted HelloRequest\n got %s\nwant %s", got, v.ClientFrame)
	}

	msgType, payload, err := tr.readFrame()
	if err != nil {
		t.Fatal(err)
	}
	if msgType != 2 || hex.EncodeToString(payload) != v.HelloResponse {
		t.Fatalf("decrypted (%d, %x), want (2, %s)", msgType, payload, v.HelloResponse)
	}
}

func TestNoiseHandshakeErrors(t *testing.T) {
	v := loadNoiseVectors(t)
	psk, _ := decodeNoiseKey(v.PSK)
	otherKey := bytes.Repeat([]byte{7}, 32)
	reject := appendNoiseFrame(nil, []byte("\x01"+invalidKeyReason))
	tests := []struct {
		name       string
		key        []byte
		fromDevice []byte
		expected   string
		want       error
	}{
		{"wrong key (response does not authenticate)", otherKey,
			append(unhex(t, v.ServerHello), unhex(t, v.ServerHandshake)...), "", ErrInvalidKey},
		{"wrong key (device rejects)", psk, append(unhex(t, v.ServerHello), reject...), "", ErrInvalidKey},
		{"other rejection", psk, append(unhex(t, v.ServerHello), appendNoiseFrame(nil, []byte("\x01Bad handshake packet len"))...), "", ErrHandshake},
		{"unknown protocol", psk, appendNoiseFrame(nil, []byte("\x02knockblock\x00")), "", ErrHandshake},
		{"unexpected name", psk, unhex(t, v.ServerHello), "kitchen", ErrBadName},
		{"plaintext device", psk, []byte{0x00, 0x00, 0x02}, "", ErrDevicePlaintext},
		{"device closes", psk, nil, "", ErrClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := noiseClientHandshake(bufio.NewReader(bytes.NewReader(tt.fromDevice)), &bytes.Buffer{},
				tt.key, tt.expected, bytes.NewReader(unhex(t, v.ClientEphemeral)))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDecodeNoiseKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if _, err := decodeNoiseKey(good); err != nil {
		t.Fatalf("valid key: %v", err)
	}
	for _, bad := range []string{"not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := decodeNoiseKey(bad); !errors.Is(err, ErrInvalidKeyFormat) {
			t.Errorf("decodeNoiseKey(%q) = %v, want ErrInvalidKeyFormat", bad, err)
		}
	}
}

func TestNoiseTransportRejectsTamperedFrame(t *testing.T) {
	v := loadNoiseVectors(t)
	psk, _ := decodeNoiseKey(v.PSK)
	frame := unhex(t, v.ServerFrame)
	frame[len(frame)-1] ^= 0xff
	fromDevice := append(append(unhex(t, v.ServerHello), unhex(t, v.ServerHandshake)...), frame...)
	tr, _, err := noiseClientHandshake(bufio.NewReader(bytes.NewReader(fromDevice)), &bytes.Buffer{},
		psk, "", bytes.NewReader(unhex(t, v.ClientEphemeral)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.readFrame(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
}

// End to end through Dial, against the fake device's Noise responder.
func TestDialNoise(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	keyB64 := base64.StdEncoding.EncodeToString(key)

	t.Run("commands work over an encrypted connection", func(t *testing.T) {
		d := newFakeDevice(t)
		d.psk = key
		c := dialFake(t, d, Options{EncryptionKey: keyB64, ExpectedName: "knockblock"})
		if c.MACAddress() != "AA:BB:CC:DD:EE:FF" {
			t.Fatalf("MAC %q", c.MACAddress())
		}
		ents, err := c.ListEntities(context.Background())
		if err != nil || len(ents) != 10 {
			t.Fatalf("ListEntities: %v, %d entities", err, len(ents))
		}
		if err := c.SetSelect(context.Background(), keyStatus, "dnd"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "status dnd", func() bool { s, _, _, _, _ := d.snapshot(); return s == "dnd" })
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "DisconnectRequest", func() bool { _, _, _, _, bye := d.snapshot(); return bye })
	})

	dialErr := func(t *testing.T, d *fakeDevice, opts Options) error {
		t.Helper()
		d.serve()
		opts.Address = d.addr()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := Dial(ctx, opts)
		if err == nil {
			c.Close()
		}
		return err
	}

	t.Run("wrong key", func(t *testing.T) {
		d := newFakeDevice(t)
		d.psk = key
		other := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32))
		if err := dialErr(t, d, Options{EncryptionKey: other}); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("err = %v, want ErrInvalidKey", err)
		}
	})
	t.Run("no key for an encrypted device", func(t *testing.T) {
		d := newFakeDevice(t)
		// An encrypted device answers a plaintext HelloRequest with a Noise
		// frame.
		d.preamble = appendNoiseFrame(nil, []byte("\x01Bad indicator byte"))
		if err := dialErr(t, d, Options{}); !errors.Is(err, ErrRequiresEncryption) {
			t.Fatalf("err = %v, want ErrRequiresEncryption", err)
		}
	})
	t.Run("malformed key", func(t *testing.T) {
		d := newFakeDevice(t)
		if err := dialErr(t, d, Options{EncryptionKey: "secret"}); !errors.Is(err, ErrInvalidKeyFormat) {
			t.Fatalf("err = %v, want ErrInvalidKeyFormat", err)
		}
	})
}
