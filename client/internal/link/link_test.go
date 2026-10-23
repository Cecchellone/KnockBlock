package link

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
)

// fakeConn is a controllable connection.
type fakeConn struct {
	mu       sync.Mutex
	done     chan struct{}
	err      error
	closed   bool
	reports  [][3]string
	noAction bool
}

func newFakeConn() *fakeConn { return &fakeConn{done: make(chan struct{})} }

func (c *fakeConn) ListEntities(context.Context) (espapi.Entities, error) {
	if c.noAction {
		return espapi.Entities{{Kind: espapi.KindSelect, ObjectID: "status"}}, nil
	}
	return espapi.Entities{{Kind: espapi.KindAction, ObjectID: "report_presence", Key: 7}}, nil
}

func (c *fakeConn) ExecuteAction(_ context.Context, a espapi.Entity, v map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return espapi.ErrClosed
	}
	if a.Key != 7 {
		return fmt.Errorf("wrong action %+v", a)
	}
	c.reports = append(c.reports, [3]string{v["pc"], v["status"], v["app"]})
	return nil
}

func (c *fakeConn) Done() <-chan struct{} { return c.done }
func (c *fakeConn) Err() error            { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

func (c *fakeConn) Close() error {
	c.drop(espapi.ErrClosed)
	return nil
}

// drop simulates the device going away.
func (c *fakeConn) drop(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed, c.err = true, err
		close(c.done)
	}
}

func (c *fakeConn) isClosed() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed }

// fakeDialer returns queued results, one per attempt.
type fakeDialer struct {
	mu       sync.Mutex
	results  []any // *fakeConn or error
	attempts int
}

func (d *fakeDialer) dial(context.Context) (Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attempts++
	if len(d.results) == 0 {
		return nil, errors.New("no device")
	}
	r := d.results[0]
	d.results = d.results[1:]
	if c, ok := r.(*fakeConn); ok {
		return c, nil
	}
	return nil, r.(error)
}

func (d *fakeDialer) push(r any) { d.mu.Lock(); defer d.mu.Unlock(); d.results = append(d.results, r) }

type stateLog struct {
	mu     sync.Mutex
	states []State
}

func (l *stateLog) add(s State) { l.mu.Lock(); defer l.mu.Unlock(); l.states = append(l.states, s) }
func (l *stateLog) last() State {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.states) == 0 {
		return State{}
	}
	return l.states[len(l.states)-1]
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

func start(t *testing.T, d *fakeDialer, log *stateLog) (*Supervisor, context.CancelFunc, chan struct{}) {
	t.Helper()
	s := New(Options{Dial: d.dial, MinBackoff: 5 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, OnState: log.add})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, cancel, done
}

func TestReconnectsAndReports(t *testing.T) {
	d := &fakeDialer{}
	d.push(errors.New("device asleep")) // first attempt fails
	c1, c2 := newFakeConn(), newFakeConn()
	d.push(c1)
	d.push(c2)
	log := &stateLog{}
	s, _, _ := start(t, d, log)

	if _, err := s.Report(context.Background(), "PC", "mic", "x"); err != nil && !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	waitFor(t, "first connection", func() bool { return log.last().Connected })
	gen1, err := s.Report(context.Background(), "PC", "call", "Teams")
	if err != nil {
		t.Fatal(err)
	}

	c1.drop(errors.New("wifi lost"))
	waitFor(t, "reconnection", func() bool {
		gen, err := s.Report(context.Background(), "PC", "call", "Teams")
		return err == nil && gen != gen1
	})
	if len(c1.reports) != 1 || c1.reports[0] != [3]string{"PC", "call", "Teams"} {
		t.Fatalf("c1 reports %v", c1.reports)
	}
	if len(c2.reports) == 0 {
		t.Fatal("nothing sent over the new connection")
	}
}

func TestConfigErrorsAreFlagged(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("wrapped: %w", espapi.ErrInvalidKey),
		espapi.ErrRequiresEncryption,
		ErrNoPresenceAction,
	} {
		d := &fakeDialer{}
		d.push(err)
		log := &stateLog{}
		start(t, d, log)
		waitFor(t, "failure state", func() bool { return log.last().Err != nil })
		if st := log.last(); !st.NeedsConfig || st.Connected {
			t.Errorf("%v: state %+v, want NeedsConfig", err, st)
		}
	}
	// A firmware without report_presence is a configuration problem too.
	d := &fakeDialer{}
	c := newFakeConn()
	c.noAction = true
	d.push(c)
	log := &stateLog{}
	start(t, d, log)
	waitFor(t, "failure state", func() bool { return log.last().Err != nil })
	if st := log.last(); !errors.Is(st.Err, ErrNoPresenceAction) || !st.NeedsConfig {
		t.Errorf("state %+v", st)
	}
	if !c.isClosed() {
		t.Error("connection without the action left open")
	}
}

func TestForceReconnect(t *testing.T) {
	d := &fakeDialer{}
	c1, c2 := newFakeConn(), newFakeConn()
	d.push(c1)
	d.push(c2)
	log := &stateLog{}
	s, _, _ := start(t, d, log)
	waitFor(t, "connection", func() bool { return log.last().Connected })
	s.ForceReconnect()
	waitFor(t, "old connection closed", c1.isClosed)
	waitFor(t, "second connection", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.attempts == 2
	})
}

func TestStopClosesConnection(t *testing.T) {
	d := &fakeDialer{}
	c := newFakeConn()
	d.push(c)
	log := &stateLog{}
	_, cancel, done := start(t, d, log)
	waitFor(t, "connection", func() bool { return log.last().Connected })
	cancel()
	<-done
	if !c.isClosed() {
		t.Fatal("connection left open after Run returned")
	}
}

func TestBackoff(t *testing.T) {
	s := New(Options{MinBackoff: time.Second, MaxBackoff: 60 * time.Second})
	for n, max := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second, 10: 60 * time.Second, 50: 60 * time.Second} {
		for i := 0; i < 50; i++ {
			if d := s.backoff(n); d < max/2 || d > max {
				t.Fatalf("backoff(%d) = %v, want in [%v, %v]", n, d, max/2, max)
			}
		}
	}
}
