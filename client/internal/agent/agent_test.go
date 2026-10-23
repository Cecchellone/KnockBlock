package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/status"
)

type fakeDetector struct {
	mu   sync.Mutex
	snap detect.Snapshot
}

func (d *fakeDetector) set(mic, cam []detect.App) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.snap = detect.Snapshot{Mic: mic, Camera: cam}
}

func (d *fakeDetector) Poll(dst *detect.Snapshot) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dst.Reset()
	dst.Mic = append(dst.Mic, d.snap.Mic...)
	dst.Camera = append(dst.Camera, d.snap.Camera...)
	return nil
}

func (d *fakeDetector) Close() error { return nil }

type fakeLink struct {
	mu      sync.Mutex
	gen     uint64
	reports []string // "pc status app"
}

func (l *fakeLink) Generation() uint64 { l.mu.Lock(); defer l.mu.Unlock(); return l.gen }

func (l *fakeLink) setGen(g uint64) { l.mu.Lock(); defer l.mu.Unlock(); l.gen = g }

func (l *fakeLink) Report(_ context.Context, pc, st, app, _ string) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reports = append(l.reports, pc+" "+st+" "+app)
	return l.gen, nil
}

func (l *fakeLink) last() (string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.reports) == 0 {
		return "", 0
	}
	return l.reports[len(l.reports)-1], len(l.reports)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestAgent(t *testing.T) {
	det := &fakeDetector{}
	link := &fakeLink{}
	var resultsMu sync.Mutex
	var results []status.Result
	a := New(Config{
		PC: "PC-STUDIO", SendApps: true, PollInterval: 5 * time.Millisecond,
		Detector: det, Link: link,
		OnResult: func(r status.Result) { resultsMu.Lock(); results = append(results, r); resultsMu.Unlock() },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()

	lastIs := func(want string) func() bool {
		return func() bool { got, _ := link.last(); return got == want }
	}

	// Not connected: nothing is sent.
	time.Sleep(30 * time.Millisecond)
	if _, n := link.last(); n != 0 {
		t.Fatalf("sent %d reports while disconnected", n)
	}
	// Connected: the current status goes out at once.
	link.setGen(1)
	waitFor(t, "first report", lastIs("PC-STUDIO free "))

	// Detection changes are reported.
	det.set([]detect.App{{Name: "Discord", Known: true}}, nil)
	waitFor(t, "mic report", lastIs("PC-STUDIO mic Discord"))
	det.set([]detect.App{{Name: "Discord", Known: true}}, []detect.App{{Name: "Teams", Known: true}})
	waitFor(t, "call report", lastIs("PC-STUDIO call Teams"))

	// No repeat while nothing changes (heartbeat is 30 s).
	_, n := link.last()
	time.Sleep(40 * time.Millisecond)
	if _, m := link.last(); m != n {
		t.Fatalf("unchanged status sent again (%d → %d reports)", n, m)
	}

	// A new connection gets the status again.
	link.setGen(2)
	waitFor(t, "report after reconnection", func() bool { _, m := link.last(); return m == n+1 })

	// The override wins at once.
	a.SetOverride(status.ForceDND)
	waitFor(t, "dnd report", lastIs("PC-STUDIO dnd "))
	if a.Current().Status != status.DND {
		t.Fatalf("Current = %+v", a.Current())
	}

	// Stopping reports offline.
	cancel()
	<-done
	if got, _ := link.last(); got != "PC-STUDIO offline " {
		t.Fatalf("last report %q, want offline", got)
	}
	resultsMu.Lock()
	defer resultsMu.Unlock()
	if len(results) < 4 || results[0].Status != status.Free {
		t.Fatalf("OnResult calls %+v", results)
	}
}
