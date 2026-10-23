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
