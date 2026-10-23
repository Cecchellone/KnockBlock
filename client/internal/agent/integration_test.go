//go:build integration

package agent

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
)

// The full PC-side path (detector → agent → link supervisor → device)
// against the host firmware; see scripts/itest-host.sh.
func TestIntegrationAgent(t *testing.T) {
	addr := os.Getenv("KNOCKBLOCK_ITEST_ADDR")
	if addr == "" {
		t.Skip("KNOCKBLOCK_ITEST_ADDR not set")
	}
	dialOpts := espapi.Options{Address: addr, EncryptionKey: os.Getenv("KNOCKBLOCK_ITEST_KEY"), ClientInfo: "KnockBlock agent test"}

	// An observer connection reads what the device shows.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	obs, err := espapi.Dial(ctx, dialOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	ents, err := obs.ListEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]uint32{}
	for _, id := range []string{"status", "active_app", "active_pc"} {
		kind := espapi.KindTextSensor
		if id == "status" {
			kind = espapi.KindSelect
		}
		e, ok := ents.Find(kind, id)
		if !ok {
			t.Fatalf("device has no %s", id)
		}
		keys[id] = e.Key
	}
	var mu sync.Mutex
	values := map[uint32]string{}
	if err := obs.SubscribeStates(ctx, func(s espapi.State) { mu.Lock(); values[s.Key] = s.Value; mu.Unlock() }); err != nil {
		t.Fatal(err)
	}
	expect := func(st, app, pc string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			mu.Lock()
			got := [3]string{values[keys["status"]], values[keys["active_app"]], values[keys["active_pc"]]}
			mu.Unlock()
			if got == [3]string{st, app, pc} {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("device shows %q, want %q", got, [3]string{st, app, pc})
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	det := &fakeDetector{}
	sup := link.New(link.Options{Dial: link.DialESPHome(dialOpts), MinBackoff: 100 * time.Millisecond})
	a := New(Config{PC: "ITEST-AGENT", SendApps: true, PollInterval: 100 * time.Millisecond, Detector: det, Link: sup})
	supCtx, stopSup := context.WithCancel(context.Background())
	supDone := make(chan struct{})
	go func() { sup.Run(supCtx); close(supDone) }()
	agentCtx, stopAgent := context.WithCancel(context.Background())
	agentDone := make(chan struct{})
	go func() { a.Run(agentCtx); close(agentDone) }()

	expect("free", "", "ITEST-AGENT")
	det.set([]detect.App{{Name: "Discord", Known: true}}, nil)
	expect("mic", "Discord", "ITEST-AGENT")
	det.set([]detect.App{{Name: "Discord", Known: true}}, []detect.App{{Name: "Teams", Known: true}})
	expect("call", "Teams", "ITEST-AGENT")

	// Stopping the agent reports offline at once.
	stopAgent()
	<-agentDone
	stopSup()
	<-supDone
	expect("offline", "", "")
}
