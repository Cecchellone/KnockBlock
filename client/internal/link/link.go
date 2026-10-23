// Package link keeps a connection to the device open for as long as the app
// runs (CLAUDE.md §5.1, step 9): it reconnects forever, silently, with
// exponential backoff and jitter, and sends presence reports over whatever
// connection is current.
package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
)

// Conn is the part of *espapi.Conn the supervisor uses.
type Conn interface {
	ListEntities(ctx context.Context) (espapi.Entities, error)
	ExecuteAction(ctx context.Context, action espapi.Entity, values map[string]string) error
	SetSelect(ctx context.Context, key uint32, option string) error
	SetSwitch(ctx context.Context, key uint32, on bool) error
	SubscribeStates(ctx context.Context, fn func(espapi.State)) error
	Done() <-chan struct{}
	Err() error
	Close() error
}

// DialFunc opens a connection.
type DialFunc func(ctx context.Context) (Conn, error)

// DialESPHome returns a DialFunc for espapi.Dial with the given options.
func DialESPHome(opts espapi.Options) DialFunc {
	return func(ctx context.Context) (Conn, error) {
		c, err := espapi.Dial(ctx, opts)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}

// Errors.
var (
	// ErrNotConnected means there is no connection right now.
	ErrNotConnected = errors.New("link: not connected")
	// ErrNoPresenceAction means the device firmware has no report_presence
	// action, i.e. it predates multi-PC presence and must be updated.
	ErrNoPresenceAction = errors.New("link: device firmware has no report_presence action")
)

// State describes the connection, for the tray tooltip and notifications.
type State struct {
	Connected bool
	// Err is why the last attempt failed or the connection dropped.
	Err error
	// NeedsConfig is true when retrying cannot help until the settings or
	// the firmware change: wrong or missing key, outdated firmware.
	NeedsConfig bool
}

// Options configures a Supervisor.
type Options struct {
	Dial DialFunc
	// MinBackoff and MaxBackoff bound the wait between attempts
	// (defaults 1 s and 60 s).
	MinBackoff, MaxBackoff time.Duration
	// OnState is called on every state change, from Run's goroutine. It
	// must not block.
	OnState func(State)
	// OnConnect, if set, runs after every successful connection, before
	// reports are sent over it (e.g. to set the display language). Errors
	// are logged, not fatal.
	OnConnect func(ctx context.Context, c Conn, ents espapi.Entities) error
	Logger    *slog.Logger
}

// Supervisor owns the connection. Run it in a goroutine; Report and
// ForceReconnect are safe to call concurrently.
type Supervisor struct {
	opts  Options
	log   *slog.Logger
	force chan struct{}

	mu     sync.Mutex
	conn   Conn
	action espapi.Entity
	gen    uint64
}

// New returns a supervisor; call Run to start it.
func New(opts Options) *Supervisor {
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = time.Second
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = 60 * time.Second
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{opts: opts, log: log, force: make(chan struct{}, 1)}
}

// Run connects and keeps reconnecting until ctx is done, then closes the
// connection gracefully and returns.
func (s *Supervisor) Run(ctx context.Context) {
	failures := 0
	for {
		conn, action, ents, err := s.connect(ctx)
		if ctx.Err() != nil {
			if conn != nil {
				conn.Close()
			}
			return
		}
		if err != nil {
			failures++
			needsConfig := isConfigError(err)
			s.state(State{Err: err, NeedsConfig: needsConfig})
			delay := s.backoff(failures)
			if needsConfig {
				delay = s.opts.MaxBackoff
			}
			s.log.Debug("connection failed", "err", err, "retry_in", delay)
			if !s.wait(ctx, delay) {
				return
			}
			continue
		}

		failures = 0
		if s.opts.OnConnect != nil {
			octx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := s.opts.OnConnect(octx, conn, ents); err != nil {
				s.log.Warn("on-connect setup failed", "err", err)
			}
			cancel()
		}
		s.mu.Lock()
		s.conn, s.action = conn, action
		s.gen++
		s.mu.Unlock()
		s.state(State{Connected: true})
		s.log.Info("connected")

		var dropped error
		select {
		case <-conn.Done():
			dropped = conn.Err()
		case <-s.force:
			dropped = errors.New("link: reconnect requested")
		case <-ctx.Done():
		}
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
		conn.Close()
		if ctx.Err() != nil {
			return
		}
		s.state(State{Err: dropped})
		s.log.Info("disconnected", "err", dropped)
		// A short pause, so a device that accepts and drops at once is not
		// hammered.
		if !s.wait(ctx, s.backoff(1)) {
			return
		}
	}
}

// connect dials and finds the report_presence action.
func (s *Supervisor) connect(ctx context.Context) (Conn, espapi.Entity, espapi.Entities, error) {
	conn, err := s.opts.Dial(ctx)
	if err != nil {
		return nil, espapi.Entity{}, nil, err
	}
	lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ents, err := conn.ListEntities(lctx)
	if err != nil {
		conn.Close()
		return nil, espapi.Entity{}, nil, fmt.Errorf("link: listing entities: %w", err)
	}
	action, ok := ents.Find(espapi.KindAction, "report_presence")
	if !ok {
		conn.Close()
		return nil, espapi.Entity{}, nil, ErrNoPresenceAction
	}
	return conn, action, ents, nil
}

// WatchDoorbell returns an OnConnect hook that calls ring, from the
// connection's reader goroutine, every time the device's doorbell event
// fires. ring must not block. Devices without the event are left alone.
func WatchDoorbell(ring func()) func(context.Context, Conn, espapi.Entities) error {
	return func(ctx context.Context, c Conn, ents espapi.Entities) error {
		ev, ok := ents.Find(espapi.KindEvent, "doorbell")
		if !ok {
			return nil
		}
		return c.SubscribeStates(ctx, func(st espapi.State) {
			if st.Key == ev.Key && st.Value == "ring" {
				ring()
			}
		})
	}
}

// Report sends this PC's presence over the current connection. It returns
// the connection generation used, which changes on every new connection.
func (s *Supervisor) Report(ctx context.Context, pc, status, app string) (uint64, error) {
	s.mu.Lock()
	conn, action, gen := s.conn, s.action, s.gen
	s.mu.Unlock()
	if conn == nil {
		return 0, ErrNotConnected
	}
	err := conn.ExecuteAction(ctx, action, map[string]string{"pc": pc, "status": status, "app": app})
	return gen, err
}

// Generation identifies the current connection: it changes on every new
// connection and is 0 while disconnected.
func (s *Supervisor) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return 0
	}
	return s.gen
}

// ForceReconnect drops the current connection, if any, and reconnects at
// once, e.g. after resume from sleep or a network change.
func (s *Supervisor) ForceReconnect() {
	select {
	case s.force <- struct{}{}:
	default:
	}
}

func (s *Supervisor) state(st State) {
	if s.opts.OnState != nil {
		s.opts.OnState(st)
	}
}

// backoff returns the wait before attempt n (1-based): exponential from
// MinBackoff, capped at MaxBackoff, with jitter in [d/2, d] so several PCs
// do not retry in lockstep.
func (s *Supervisor) backoff(n int) time.Duration {
	d := s.opts.MinBackoff
	for i := 1; i < n && d < s.opts.MaxBackoff; i++ {
		d *= 2
	}
	d = min(d, s.opts.MaxBackoff)
	return d/2 + rand.N(d/2+1)
}

// wait sleeps for d, returning early on a forced reconnect; false when ctx
// is done.
func (s *Supervisor) wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.force:
		return true
	case <-ctx.Done():
		return false
	}
}

// isConfigError reports errors that retrying cannot fix by itself.
func isConfigError(err error) bool {
	return errors.Is(err, espapi.ErrInvalidKey) || errors.Is(err, espapi.ErrInvalidKeyFormat) ||
		errors.Is(err, espapi.ErrRequiresEncryption) || errors.Is(err, espapi.ErrDevicePlaintext) ||
		errors.Is(err, ErrNoPresenceAction)
}
