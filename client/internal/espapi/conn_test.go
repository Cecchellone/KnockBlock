package espapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi/pb"
)

func dialFake(t *testing.T, d *fakeDevice, opts Options) *Conn {
	t.Helper()
	d.serve()
	opts.Address = d.addr()
	if opts.ClientInfo == "" {
		opts.ClientInfo = "KnockBlock test"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDialHello(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{ClientInfo: "KnockBlock 1.0", ExpectedName: "knockblock"})
	if c.DeviceName() != "knockblock" || c.ServerInfo() != "fake 2026.10.0" {
		t.Fatalf("name %q, server info %q", c.DeviceName(), c.ServerInfo())
	}
	if major, minor := c.APIVersion(); major != 1 || minor != 19 {
		t.Fatalf("API %d.%d", major, minor)
	}
	d.mu.Lock()
	hello := d.hello
	d.mu.Unlock()
	if hello.GetClientInfo() != "KnockBlock 1.0" || hello.GetApiVersionMajor() != 1 || hello.GetApiVersionMinor() != 19 {
		t.Fatalf("hello = %v", hello)
	}
}

func TestDialErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeDevice)
		opts  Options
		want  error
	}{
		{"wrong name", func(*fakeDevice) {}, Options{ExpectedName: "other"}, ErrBadName},
		{"incompatible API", func(d *fakeDevice) { d.apiMajor = 3 }, Options{}, ErrIncompatibleAPI},
		// A Noise device answers a plaintext client with a 0x01 frame.
		{"device requires encryption", func(d *fakeDevice) { d.preamble = []byte{0x01, 0x00, 0x00} }, Options{}, ErrRequiresEncryption},
		{"device closes", func(d *fakeDevice) { d.preamble = []byte{} }, Options{}, ErrClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newFakeDevice(t)
			tt.setup(d)
			d.serve()
			tt.opts.Address = d.addr()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := Dial(ctx, tt.opts)
			if err == nil {
				c.Close()
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDialCancelledDuringHello(t *testing.T) {
	// A listener that accepts but never answers.
	d := newFakeDevice(t)
	go func() {
		nc, err := d.ln.Accept()
		if err == nil {
			t.Cleanup(func() { nc.Close() })
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := Dial(ctx, Options{Address: d.addr()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestListEntities(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	ents, err := c.ListEntities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 8 {
		t.Fatalf("got %d entities, want 8 (sensor skipped): %+v", len(ents), ents)
	}
	tests := []struct {
		kind     EntityKind
		objectID string
		key      uint32
	}{
		{KindSelect, "status", keyStatus},
		{KindSelect, "display_language", keyLanguage},
		{KindButton, "heartbeat", keyHeartbeat},
		{KindTextSensor, "status_label", keyStatusLabel},
		{KindAction, "report_presence", keyReport},
		{KindSwitch, "flip_display", keyFlip},
		{KindEvent, "doorbell", keyDoorbell},
		{KindButton, "ring_doorbell", keyRing},
	}
	for _, tt := range tests {
		e, ok := ents.Find(tt.kind, tt.objectID)
		if !ok || e.Key != tt.key {
			t.Errorf("Find(%v, %q) = %+v, %v", tt.kind, tt.objectID, e, ok)
		}
	}
	if e, _ := ents.Find(KindSelect, "status"); len(e.Options) != 6 {
		t.Errorf("status options = %v", e.Options)
	}
	if e, _ := ents.Find(KindEvent, "doorbell"); len(e.Options) != 1 || e.Options[0] != "ring" {
		t.Errorf("doorbell event types = %v", e.Options)
	}
	// A second listing works too.
	if _, err := c.ListEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStatesAndCommands(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	states := make(chan State, 16)
	if err := c.SubscribeStates(context.Background(), func(s State) { states <- s }); err != nil {
		t.Fatal(err)
	}
	expect := func(key uint32, value string) {
		t.Helper()
		select {
		case s := <-states:
			if s.Key != key || s.Value != value {
				t.Fatalf("state %+v, want key %d = %q", s, key, value)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no state for key %d", key)
		}
	}
	expect(keyStatus, "offline")
	expect(keyLanguage, "en")
	expect(keyFlip, "off")

	ctx := context.Background()
	if err := c.SetSelect(ctx, keyStatus, "call"); err != nil {
		t.Fatal(err)
	}
	expect(keyStatus, "call")
	if err := c.SetSelect(ctx, keyLanguage, "it"); err != nil {
		t.Fatal(err)
	}
	expect(keyLanguage, "it")
	if err := c.PressButton(ctx, keyHeartbeat); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "button press", func() bool { _, _, presses, _, _ := d.snapshot(); return presses == 1 })
	if err := c.SetSwitch(ctx, keyFlip, true); err != nil {
		t.Fatal(err)
	}
	expect(keyFlip, "on")
	// Events arrive as states carrying the event type.
	if err := c.PressButton(ctx, keyRing); err != nil {
		t.Fatal(err)
	}
	expect(keyDoorbell, "ring")
	if status, language, _, _, _ := d.snapshot(); status != "call" || language != "it" {
		t.Fatalf("device state %q %q", status, language)
	}
}

func TestUnknownMessagesAreSkipped(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	d.sendRaw(250, []byte{0xde, 0xad}) // no such message
	d.send(&pb.DeviceInfoResponse{Name: "knockblock"})
	if _, err := c.ListEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAnswersDevicePing(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	d.send(&pb.PingRequest{})
	waitFor(t, "PingResponse", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.pongs == 1
	})
	if c.Err() != nil {
		t.Fatalf("connection closed: %v", c.Err())
	}
}

func TestKeepAlivePingsIdleDevice(t *testing.T) {
	d := newFakeDevice(t)
	dialFake(t, d, Options{KeepAlive: 40 * time.Millisecond})
	waitFor(t, "keepalive pings", func() bool { _, _, _, pings, _ := d.snapshot(); return pings >= 2 })
}

func TestKeepAliveTimeout(t *testing.T) {
	d := newFakeDevice(t)
	d.ignorePings = true
	c := dialFake(t, d, Options{KeepAlive: 20 * time.Millisecond})
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection to a silent device was not closed")
	}
	if !errors.Is(c.Err(), ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", c.Err())
	}
}

func TestDeviceDisconnect(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	d.send(&pb.DisconnectRequest{})
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not closed after DisconnectRequest")
	}
	if !errors.Is(c.Err(), ErrDisconnected) {
		t.Fatalf("err = %v, want ErrDisconnected", c.Err())
	}
	if err := c.SetSelect(context.Background(), keyStatus, "free"); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("command after disconnect: %v", err)
	}
}

func TestGracefulClose(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Close waited for the disconnect timeout instead of the response")
	}
	waitFor(t, "DisconnectRequest", func() bool { _, _, _, _, bye := d.snapshot(); return bye })
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", c.Err())
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestExecuteAction(t *testing.T) {
	d := newFakeDevice(t)
	c := dialFake(t, d, Options{})
	ctx := context.Background()
	ents, err := c.ListEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report, ok := ents.Find(KindAction, "report_presence")
	if !ok || len(report.Args) != 3 || report.Args[0].Name != "pc" {
		t.Fatalf("report_presence = %+v, %v", report, ok)
	}
	if err := c.ExecuteAction(ctx, report, map[string]string{"pc": "PC-STUDIO", "status": "call", "app": "Teams"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "report", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.reports) == 1
	})
	d.mu.Lock()
	got := d.reports[0]
	d.mu.Unlock()
	if len(got) != 3 || got[0] != "PC-STUDIO" || got[1] != "call" || got[2] != "Teams" {
		t.Fatalf("device got %q; arguments must follow the declared order", got)
	}

	bad := []struct {
		name   string
		entity Entity
		values map[string]string
	}{
		{"missing argument", report, map[string]string{"pc": "a", "status": "free"}},
		{"unknown argument", report, map[string]string{"pc": "a", "status": "free", "ap": "x"}},
		{"not an action", Entity{Kind: KindButton, ObjectID: "heartbeat"}, nil},
		{"non-string argument", Entity{Kind: KindAction, ObjectID: "x", Args: []ActionArg{{Name: "n", Type: pb.ServiceArgType_SERVICE_ARG_TYPE_INT}}}, map[string]string{"n": "1"}},
	}
	for _, tt := range bad {
		if err := c.ExecuteAction(ctx, tt.entity, tt.values); err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}
