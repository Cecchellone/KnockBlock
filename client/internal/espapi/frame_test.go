package espapi

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi/pb"
)

// Expected frames were produced with aioesphomeapi 46.12
// (_frame_helper/packets.py, make_plain_text_packets).
func TestPlaintextFrameVectors(t *testing.T) {
	hello, _ := proto.Marshal(&pb.HelloRequest{ClientInfo: "KnockBlock", ApiVersionMajor: 1, ApiVersionMinor: 19})
	selectCmd, _ := proto.Marshal(&pb.SelectCommandRequest{Key: 0x12345678, State: "call"})
	tests := []struct {
		name    string
		msgType uint32
		payload []byte
		want    string
	}{
		{"empty ping", 7, nil, "000007"},
		{"hello", 1, hello, "0010010a0a4b6e6f636b426c6f636b10011813"},
		{"select command", 54, selectCmd, "000b360d78563412120463616c6c"},
		{"two-byte varints", 300, bytes.Repeat([]byte("x"), 200), "00c801ac02" + strings.Repeat("78", 200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tr := newPlaintextTransport(nil, &out)
			if err := tr.writeFrame(tt.msgType, tt.payload); err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(out.Bytes()); got != tt.want {
				t.Fatalf("frame = %s, want %s", got, tt.want)
			}

			// And it reads back.
			rt := newPlaintextTransport(bufio.NewReader(&out), nil)
			gotType, gotPayload, err := rt.readFrame()
			if err != nil {
				t.Fatal(err)
			}
			if gotType != tt.msgType || !bytes.Equal(gotPayload, tt.payload) {
				t.Fatalf("read (%d, %x), want (%d, %x)", gotType, gotPayload, tt.msgType, tt.payload)
			}
		})
	}
}

func TestPlaintextReadErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string // hex
		want  error
	}{
		{"noise preamble", "010000", ErrRequiresEncryption},
		{"invalid preamble", "050000", ErrProtocol},
		{"frame too long", "00808004" + "07", ErrProtocol}, // length 65536
		{"varint too long", "00ffffffff01", ErrProtocol},
		{"truncated payload", "000507aabb", ErrClosed},
		{"empty input", "", ErrClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := hex.DecodeString(tt.input)
			tr := newPlaintextTransport(bufio.NewReader(bytes.NewReader(raw)), nil)
			_, _, err := tr.readFrame()
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPlaintextReadsConsecutiveFrames(t *testing.T) {
	raw, _ := hex.DecodeString("000007" + "000008" + "000b360d78563412120463616c6c")
	tr := newPlaintextTransport(bufio.NewReader(bytes.NewReader(raw)), nil)
	for _, want := range []uint32{7, 8, 54} {
		got, _, err := tr.readFrame()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("type = %d, want %d", got, want)
		}
	}
}

func FuzzPlaintextReadFrame(f *testing.F) {
	for _, seed := range []string{"000007", "0010010a0a4b6e6f636b426c6f636b10011813", "00808004", "00ffffffff01", "01"} {
		raw, _ := hex.DecodeString(seed)
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tr := newPlaintextTransport(bufio.NewReader(bytes.NewReader(data)), nil)
		for {
			_, payload, err := tr.readFrame()
			if err != nil {
				return
			}
			if len(payload) > maxFrameSize {
				t.Fatalf("payload of %d bytes", len(payload))
			}
		}
	})
}
