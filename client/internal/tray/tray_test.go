package tray

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
	"github.com/Cecchellone/KnockBlock/client/internal/status"
	"github.com/Cecchellone/KnockBlock/client/locales"
)

func translator(t *testing.T, lang string) *i18n.Translator {
	t.Helper()
	c, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c.Translator(lang)
}

func TestRender(t *testing.T) {
	en, it := translator(t, "en"), translator(t, "it")
	connected := link.State{Connected: true}
	tests := []struct {
		name       string
		tr         *i18n.Translator
		snap       Snapshot
		status     string
		connection string
		color      bool // status colour (true) or grey
	}{
		{"call with app", en, Snapshot{status.Result{Status: status.Call, App: "Teams"}, connected, "knockblock.local"},
			"In a call (Teams)", "Connected to knockblock.local", true},
		{"italian", it, Snapshot{status.Result{Status: status.Mic, App: "Discord"}, connected, "luce.lan"},
			"Microfono acceso (Discord)", "Connesso a luce.lan", true},
		{"free, no app", en, Snapshot{status.Result{Status: status.Free}, connected, "x"}, "Free", "Connected to x", true},
		{"connecting", en, Snapshot{status.Result{Status: status.Free}, link.State{}, "x"}, "Free", "Connecting to x…", false},
		{"unreachable", en, Snapshot{status.Result{Status: status.Call}, link.State{Err: errors.New("timeout")}, "x"},
			"In a call", "x unreachable, retrying", false},
		{"bad key", it, Snapshot{status.Result{Status: status.Free}, link.State{Err: espapi.ErrInvalidKey, NeedsConfig: true}, "x"},
			"Libero", "Chiave di cifratura errata o mancante per x", false},
		{"old firmware", en, Snapshot{status.Result{Status: status.Free}, link.State{Err: link.ErrNoPresenceAction, NeedsConfig: true}, "x"},
			"Free", "x needs a firmware update", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Render(tt.tr, tt.snap)
			if v.Status != tt.status || v.Connection != tt.connection {
				t.Errorf("got %q / %q, want %q / %q", v.Status, v.Connection, tt.status, tt.connection)
			}
			if !strings.Contains(v.Tooltip, tt.status) || !strings.Contains(v.Tooltip, tt.connection) {
				t.Errorf("tooltip %q", v.Tooltip)
			}
			if wantColor := ColorFor(tt.snap.Result.Status, true); (v.Color == wantColor) != tt.color && v.Color != Disconnected {
				t.Errorf("colour %v", v.Color)
			}
			if !tt.color && v.Color != Disconnected {
				t.Errorf("colour %v, want grey", v.Color)
			}
		})
	}
	long := Render(en, Snapshot{status.Result{Status: status.Call, App: strings.Repeat("A", 200)}, connected, "x"})
	if n := len([]rune(long.Tooltip)); n > maxTooltip {
		t.Errorf("tooltip of %d runes", n)
	}
}

func TestAlerts(t *testing.T) {
	var a Alerts
	t0 := time.Unix(0, 0)
	down := link.State{Err: errors.New("timeout")}
	steps := []struct {
		at    time.Duration
		state link.State
		want  string // alert key, "" for none
	}{
		{0, link.State{}, ""}, // first connection attempt
		{1 * time.Second, down, ""},
		{4 * time.Minute, down, ""},
		{5*time.Minute + time.Second, down, "notify.unreachable"},
		{6 * time.Minute, down, ""}, // once
		{7 * time.Minute, link.State{Connected: true}, ""},
		{8 * time.Minute, link.State{Err: espapi.ErrInvalidKey, NeedsConfig: true}, "notify.bad_key"},
		{9 * time.Minute, link.State{Err: espapi.ErrInvalidKey, NeedsConfig: true}, ""},
		{10 * time.Minute, link.State{Connected: true}, ""},
		{11 * time.Minute, link.State{Err: link.ErrNoPresenceAction, NeedsConfig: true}, "notify.old_firmware"},
	}
	for _, s := range steps {
		alert, ok := a.Update(s.state, t0.Add(s.at))
		got := ""
		if ok {
			got = alert.Key
		}
		if got != s.want {
			t.Fatalf("at %v: alert %q, want %q", s.at, got, s.want)
		}
	}
}

// engineConn is a minimal device connection for engine tests.
type engineConn struct {
	mu      sync.Mutex
	reports []string
	selects []string
	onState func(espapi.State)
	done    chan struct{}
}

func (c *engineConn) ListEntities(context.Context) (espapi.Entities, error) {
	return espapi.Entities{
		{Kind: espapi.KindAction, ObjectID: "report_presence", Key: 1},
		{Kind: espapi.KindSelect, ObjectID: "display_language", Key: 2, Options: []string{"en", "it"}},
		{Kind: espapi.KindEvent, ObjectID: "doorbell", Key: 3, Options: []string{"ring"}},
	}, nil
}

func (c *engineConn) SetSwitch(context.Context, uint32, bool) error { return nil }

func (c *engineConn) SubscribeStates(_ context.Context, fn func(espapi.State)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onState = fn
	return nil
}

func (c *engineConn) ring() {
	c.mu.Lock()
	fn := c.onState
	c.mu.Unlock()
	if fn != nil {
		fn(espapi.State{Key: 3, Value: "ring"})
	}
}

func (c *engineConn) ExecuteAction(_ context.Context, _ espapi.Entity, v map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, v["pc"]+" "+v["status"]+" "+v["app"])
	return nil
}

func (c *engineConn) SetSelect(_ context.Context, _ uint32, o string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.selects = append(c.selects, o)
	return nil
}

func (c *engineConn) Done() <-chan struct{} { return c.done }
func (c *engineConn) Err() error            { return nil }
func (c *engineConn) Close() error          { return nil }

func (c *engineConn) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) == 0 {
		return ""
	}
	return c.reports[len(c.reports)-1]
}

func TestEngine(t *testing.T) {
	conn := &engineConn{done: make(chan struct{})}
	var dialed []espapi.Options
	var mu sync.Mutex
	var rings int
	apps := filepath.Join(t.TempDir(), "apps.txt")
	os.WriteFile(apps, []byte("mic Discord.exe\n"), 0o644)
	e := NewEngine(Deps{
		NewDetector: func(o detect.Options) (detect.Detector, error) { return detect.FromFile(apps, o), nil },
		Dial: func(o espapi.Options) link.DialFunc {
			mu.Lock()
			dialed = append(dialed, o)
			mu.Unlock()
			return func(context.Context) (link.Conn, error) { return conn, nil }
		},
		OnDoorbell: func() { mu.Lock(); rings++; mu.Unlock() },
		ClientInfo: "test",
	})
	cfg := config.Default()
	cfg.Host = "light.lan"
	cfg.PCName = "DESK"
	cfg.AppNames = map[string]string{"discord.exe": "Disco"}
	if err := cfg.SetKey("k"); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for conn.last() != want {
			if time.Now().After(deadline) {
				t.Fatalf("last report %q, want %q", conn.last(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("DESK mic Disco")
	conn.ring()
	mu.Lock()
	if rings != 1 {
		t.Errorf("%d doorbell rings, want 1", rings)
	}
	mu.Unlock()
	mu.Lock()
	if dialed[0].Address != "light.lan:6053" || dialed[0].EncryptionKey != "k" || dialed[0].ClientInfo != "test" {
		t.Errorf("dial options %+v", dialed[0])
	}
	mu.Unlock()

	e.SetOverride(status.ForceDND)
	waitFor("DESK dnd ")

	// A new name, on the same device, is applied without reconnecting: the
	// old name is reported offline first and the override survives.
	cfg.PCName = "LAPTOP"
	if err := e.Start(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor("LAPTOP dnd ")
	conn.mu.Lock()
	sawOffline := false
	for _, r := range conn.reports {
		if r == "DESK offline " {
			sawOffline = true
		}
	}
	conn.mu.Unlock()
	if !sawOffline {
		t.Error("old PC name not reported offline on restart")
	}
	mu.Lock()
	if len(dialed) != 1 {
		t.Errorf("%d connections for a PC name change, want 1", len(dialed))
	}
	mu.Unlock()

	// Turning the doorbell off needs no reconnection either.
	cfg.Doorbell = false
	if err := e.Start(cfg); err != nil {
		t.Fatal(err)
	}
	conn.ring()
	mu.Lock()
	if rings != 1 || len(dialed) != 1 {
		t.Errorf("doorbell off: %d rings, %d connections", rings, len(dialed))
	}
	mu.Unlock()

	// A new address reconnects.
	cfg.Host = "other.lan"
	if err := e.Start(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor("LAPTOP dnd ")
	mu.Lock()
	if len(dialed) != 2 || dialed[1].Address != "other.lan:6053" {
		t.Errorf("new address: dialed %+v", dialed)
	}
	mu.Unlock()
	e.Stop()
	if conn.last() != "LAPTOP offline " {
		t.Errorf("stop did not report offline: %q", conn.last())
	}
}

func TestChime(t *testing.T) {
	w := Chime()
	if string(w[0:4]) != "RIFF" || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" {
		t.Fatalf("bad header % x", w[:44])
	}
	if got := binary.LittleEndian.Uint32(w[40:]); int(got) != len(w)-44 {
		t.Fatalf("data size %d, file has %d", got, len(w)-44)
	}
	if len(w) > 24<<10 {
		t.Errorf("chime is %d bytes", len(w))
	}
	lo, hi := byte(255), byte(0)
	for _, b := range w[44:] {
		lo, hi = min(lo, b), max(hi, b)
	}
	if lo < 10 || hi > 245 || hi-lo < 100 {
		t.Errorf("samples in [%d, %d]: clipped or silent", lo, hi)
	}
}
