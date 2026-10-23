package espapi

import (
	"encoding/base64"
	"net"
	"os"
	"testing"
	"time"
)

// TestInteropFakeDevice serves the fake device with Noise on
// KNOCKBLOCK_INTEROP_ADDR so that aioesphomeapi, the reference client, can
// connect to it (testdata/interop_aioesphomeapi.py). It checks that this
// package's Noise framing interoperates with the reference implementation
// where no encrypted firmware can be built. Skipped unless the variable is
// set.
func TestInteropFakeDevice(t *testing.T) {
	addr := os.Getenv("KNOCKBLOCK_INTEROP_ADDR")
	if addr == "" {
		t.Skip("KNOCKBLOCK_INTEROP_ADDR not set")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("KNOCKBLOCK_INTEROP_KEY"))
	if err != nil || len(key) != 32 {
		t.Fatalf("KNOCKBLOCK_INTEROP_KEY must be a base64 32-byte key")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	d := newFakeDevice(t)
	d.ln.Close()
	d.ln = ln
	d.psk = key
	d.serve()
	select {
	case <-d.connected:
	case <-time.After(30 * time.Second):
		t.Fatal("no client connected")
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, _, _, _, bye := d.snapshot(); bye {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client did not disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, language, presses, _, _ := d.snapshot()
	if status != "call" || language != "it" || presses != 1 {
		t.Fatalf("device state: status %q, language %q, presses %d", status, language, presses)
	}
}
