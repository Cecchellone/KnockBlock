//go:build integration

// Integration tests against a real ESPHome firmware: firmware/host-test.yaml
// running on ESPHome's host platform. Run them with scripts/itest-host.sh,
// or by hand:
//
//	KNOCKBLOCK_ITEST_ADDR=127.0.0.1:6053 go test -tags integration ./internal/espapi/
//
// KNOCKBLOCK_ITEST_KEY sets the encryption key; KNOCKBLOCK_ITEST_WATCHDOG
// the device watchdog timeout (default 10s, as in host-test.yaml).
package espapi

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func integrationConn(t *testing.T) (*Conn, Entities) {
	t.Helper()
	addr := os.Getenv("KNOCKBLOCK_ITEST_ADDR")
	if addr == "" {
		t.Skip("KNOCKBLOCK_ITEST_ADDR not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Dial(ctx, Options{
		Address:       addr,
		EncryptionKey: os.Getenv("KNOCKBLOCK_ITEST_KEY"),
		ClientInfo:    "KnockBlock integration test",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	ents, err := c.ListEntities(ctx)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	return c, ents
}

// stateTracker records the latest value of every entity.
type stateTracker struct {
	mu     sync.Mutex
	values map[uint32]string
}

func (s *stateTracker) set(st State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[st.Key] = st.Value
}

func (s *stateTracker) get(key uint32) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func (s *stateTracker) waitFor(t *testing.T, key uint32, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for s.get(key) != want {
		if time.Now().After(deadline) {
			t.Fatalf("key %d is %q, want %q after %v", key, s.get(key), want, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// labels reads firmware/locales/<lang>.yaml ("status: label" lines), so the
// test follows the translations.
func labels(t *testing.T, lang string) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "firmware", "locales", lang+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

func TestIntegrationStatusLanguageWatchdog(t *testing.T) {
	c, ents := integrationConn(t)
	find := func(kind EntityKind, id string) uint32 {
		t.Helper()
		e, ok := ents.Find(kind, id)
		if !ok {
			t.Fatalf("device has no %v %q; entities: %+v", kind, id, ents)
		}
		return e.Key
	}
	status := find(KindSelect, "status")
	language := find(KindSelect, "display_language")
	heartbeat := find(KindButton, "heartbeat")
	label := find(KindTextSensor, "status_label")

	states := &stateTracker{values: map[uint32]string{}}
	ctx := context.Background()
	if err := c.SubscribeStates(ctx, states.set); err != nil {
		t.Fatal(err)
	}
	en, it := labels(t, "en"), labels(t, "it")

	// Changing the status updates the localized label (M3 acceptance).
	must(t, c.SetSelect(ctx, language, "en"))
	states.waitFor(t, language, "en", 5*time.Second)
	must(t, c.SetSelect(ctx, status, "call"))
	states.waitFor(t, status, "call", 5*time.Second)
	states.waitFor(t, label, en["call"], 5*time.Second)

	must(t, c.SetSelect(ctx, language, "it"))
	states.waitFor(t, language, "it", 5*time.Second)
	states.waitFor(t, label, it["call"], 5*time.Second)

	// A heartbeat keeps the status; without heartbeats the watchdog sets
	// offline.
	watchdog := 10 * time.Second
	if v := os.Getenv("KNOCKBLOCK_ITEST_WATCHDOG"); v != "" {
		d, err := time.ParseDuration(v)
		must(t, err)
		watchdog = d
	}
	time.Sleep(watchdog / 2)
	must(t, c.PressButton(ctx, heartbeat))
	time.Sleep(watchdog * 3 / 4)
	if got := states.get(status); got != "call" {
		t.Fatalf("status %q after a heartbeat, want call", got)
	}
	states.waitFor(t, status, "offline", watchdog)
	states.waitFor(t, label, it["offline"], 5*time.Second)

	// Leave the device in its default language.
	must(t, c.SetSelect(ctx, language, "en"))
	states.waitFor(t, language, "en", 5*time.Second)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// With an encrypted device, a wrong key and no key are both rejected.
func TestIntegrationKeyErrors(t *testing.T) {
	addr := os.Getenv("KNOCKBLOCK_ITEST_ADDR")
	if addr == "" || os.Getenv("KNOCKBLOCK_ITEST_KEY") == "" {
		t.Skip("needs KNOCKBLOCK_ITEST_ADDR and KNOCKBLOCK_ITEST_KEY")
	}
	tests := []struct {
		name string
		key  string
		want error
	}{
		{"wrong key", "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=", ErrInvalidKey},
		{"no key", "", ErrRequiresEncryption},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, err := Dial(ctx, Options{Address: addr, EncryptionKey: tt.key, ClientInfo: "KnockBlock integration test"})
			if err == nil {
				c.Close()
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Several PCs report through report_presence: the strongest live status
// wins, with its app and PC; a PC that stops reporting expires.
func TestIntegrationPresence(t *testing.T) {
	c, ents := integrationConn(t)
	ctx := context.Background()
	find := func(kind EntityKind, id string) Entity {
		t.Helper()
		e, ok := ents.Find(kind, id)
		if !ok {
			t.Fatalf("device has no %v %q", kind, id)
		}
		return e
	}
	status := find(KindSelect, "status").Key
	app := find(KindTextSensor, "active_app").Key
	pc := find(KindTextSensor, "active_pc").Key
	report := find(KindAction, "report_presence")

	states := &stateTracker{values: map[uint32]string{}}
	must(t, c.SubscribeStates(ctx, states.set))
	send := func(pcName, st, appName string) {
		t.Helper()
		must(t, c.ExecuteAction(ctx, report, map[string]string{"pc": pcName, "status": st, "app": appName}))
	}
	expect := func(st, appName, pcName string, timeout time.Duration) {
		t.Helper()
		states.waitFor(t, status, st, timeout)
		states.waitFor(t, app, appName, time.Second)
		states.waitFor(t, pc, pcName, time.Second)
	}

	send("PC-STUDIO", "mic", "Discord")
	expect("mic", "Discord", "PC-STUDIO", 5*time.Second)
	send("LAPTOP", "call", "Teams")
	expect("call", "Teams", "LAPTOP", 5*time.Second)

	// An unknown status is ignored.
	send("PC-STUDIO", "bogus", "x")
	time.Sleep(500 * time.Millisecond)
	expect("call", "Teams", "LAPTOP", time.Second)

	// LAPTOP stops reporting while PC-STUDIO keeps going: LAPTOP expires.
	watchdog := 10 * time.Second
	if v := os.Getenv("KNOCKBLOCK_ITEST_WATCHDOG"); v != "" {
		d, err := time.ParseDuration(v)
		must(t, err)
		watchdog = d
	}
	deadline := time.Now().Add(watchdog + 3*time.Second)
	for time.Now().Before(deadline) && states.get(status) == "call" {
		send("PC-STUDIO", "mic", "Discord")
		time.Sleep(watchdog / 4)
	}
	expect("mic", "Discord", "PC-STUDIO", time.Second)

	// Reporting offline removes the PC at once.
	send("PC-STUDIO", "offline", "")
	expect("offline", "", "", 5*time.Second)
}

// The doorbell event reaches subscribed clients once per ring, presses
// within the cooldown count once, and the flip switch round-trips.
func TestIntegrationDoorbellAndFlip(t *testing.T) {
	c, ents := integrationConn(t)
	ring, ok := ents.Find(KindButton, "ring_doorbell")
	doorbell, ok2 := ents.Find(KindEvent, "doorbell")
	flip, ok3 := ents.Find(KindSwitch, "flip_display")
	if !ok || !ok2 || !ok3 {
		t.Fatalf("missing ring_doorbell/doorbell/flip_display in %+v", ents)
	}
	var mu sync.Mutex
	rings := 0
	states := &stateTracker{values: map[uint32]string{}}
	ctx := context.Background()
	must(t, c.SubscribeStates(ctx, func(st State) {
		if st.Key == doorbell.Key {
			mu.Lock()
			rings++
			mu.Unlock()
			return
		}
		states.set(st)
	}))
	count := func() int { mu.Lock(); defer mu.Unlock(); return rings }

	// No event is replayed on subscription.
	states.waitFor(t, flip.Key, "off", 5*time.Second)
	if n := count(); n != 0 {
		t.Fatalf("%d rings before any press", n)
	}
	must(t, c.PressButton(ctx, ring.Key))
	must(t, c.PressButton(ctx, ring.Key)) // within the 3 s cooldown
	deadline := time.Now().Add(5 * time.Second)
	for count() < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if n := count(); n != 1 {
		t.Fatalf("%d rings for two quick presses, want 1", n)
	}
	time.Sleep(3 * time.Second)
	must(t, c.PressButton(ctx, ring.Key))
	deadline = time.Now().Add(5 * time.Second)
	for count() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := count(); n != 2 {
		t.Fatalf("%d rings after the cooldown, want 2", n)
	}

	must(t, c.SetSwitch(ctx, flip.Key, true))
	states.waitFor(t, flip.Key, "on", 5*time.Second)
	must(t, c.SetSwitch(ctx, flip.Key, false))
	states.waitFor(t, flip.Key, "off", 5*time.Second)
}
