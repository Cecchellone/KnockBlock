// Package agent runs this PC's presence loop: poll the detector, compute
// the status, and report it to the device when it changes, after every
// reconnection and as a heartbeat (CLAUDE.md §5.3). The tray app and
// `knockctl agent` both run it.
package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/status"
)

// Link sends reports; *link.Supervisor implements it.
type Link interface {
	// Generation identifies the current connection, changing on every new
	// one; 0 means not connected.
	Generation() uint64
	Report(ctx context.Context, pc, status, app, devices string) (gen uint64, err error)
}

// Config configures an Agent.
type Config struct {
	PC       string // name reported for this PC
	SendApps bool   // false omits app names (privacy setting)
	// PollInterval is how often the detector is read (default 1 s, §5.2).
	PollInterval time.Duration
	Detector     detect.Detector
	Link         Link
	// OnResult is called, from Run's goroutine, whenever the computed
	// result changes. It must not block.
	OnResult func(status.Result)
	Logger   *slog.Logger
}

// Agent is the presence loop. Run it in a goroutine; SetOverride and
// Current are safe for concurrent use.
type Agent struct {
	cfg  Config
	log  *slog.Logger
	poke chan struct{}

	mu       sync.Mutex
	override status.Override
	current  status.Result
	update   *update // applied by Run
}

type update struct {
	pc       string
	sendApps bool
	det      detect.Detector
}

// New returns an agent; call Run to start it.
func New(cfg Config) *Agent {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Agent{cfg: cfg, log: log, poke: make(chan struct{}, 1)}
}

// SetOverride sets the manual override from the tray menu and re-evaluates
// at once.
func (a *Agent) SetOverride(o status.Override) {
	a.mu.Lock()
	a.override = o
	a.mu.Unlock()
	select {
	case a.poke <- struct{}{}:
	default:
	}
}

// Update changes the PC name, the app-name setting and the detector while
// running, on the same connection. Run reports the old PC name offline if
// it changed, closes the old detector and reports the status again.
func (a *Agent) Update(pc string, sendApps bool, det detect.Detector) {
	a.mu.Lock()
	if a.update != nil && a.update.det != det {
		a.update.det.Close() // replaced before Run took it
	}
	a.update = &update{pc: pc, sendApps: sendApps, det: det}
	a.mu.Unlock()
	select {
	case a.poke <- struct{}{}:
	default:
	}
}

// Current returns the last computed result.
func (a *Agent) Current() status.Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// Run polls until ctx is done, then reports `offline` for this PC, so the
// light updates at once instead of after the device timeout.
func (a *Agent) Run(ctx context.Context) {
	var (
		snap     detect.Snapshot
		reporter status.Reporter
		failing  bool
	)
	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		u := a.update
		a.update = nil
		a.mu.Unlock()
		if u != nil {
			if u.pc != a.cfg.PC {
				a.sendOffline()
			}
			if u.det != a.cfg.Detector {
				a.cfg.Detector.Close()
			}
			a.cfg.PC, a.cfg.SendApps, a.cfg.Detector = u.pc, u.sendApps, u.det
			reporter = status.Reporter{} // report again with the new settings
		}
		if err := a.cfg.Detector.Poll(&snap); err != nil {
			if !failing {
				a.log.Warn("detection failed", "err", err)
			}
			failing = true
			snap.Reset() // report free rather than a stale status
		} else {
			failing = false
		}

		a.mu.Lock()
		res := status.Compute(&snap, a.override, a.cfg.SendApps)
		changed := res != a.current
		a.current = res
		a.mu.Unlock()
		if changed {
			a.log.Info("status", "status", res.Status, "app", res.App)
			if a.cfg.OnResult != nil {
				a.cfg.OnResult(res)
			}
		}

		a.report(ctx, &reporter, res)

		select {
		case <-ctx.Done():
			a.sendOffline()
			return
		case <-ticker.C:
		case <-a.poke:
		}
	}
}

// report sends res when the reporter says it is due: on change, on a new
// connection, or as a heartbeat. Nothing is sent while disconnected; the
// next connection's generation makes the result due again.
func (a *Agent) report(ctx context.Context, r *status.Reporter, res status.Result) {
	now := time.Now()
	gen := a.cfg.Link.Generation()
	if gen == 0 || !r.Due(res, now, gen) {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sentGen, err := a.cfg.Link.Report(sctx, a.cfg.PC, res.Status, res.App, res.Devices())
	if err != nil {
		a.log.Debug("report not sent", "err", err)
		return
	}
	r.Sent(res, now, sentGen)
}

func (a *Agent) sendOffline() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := a.cfg.Link.Report(ctx, a.cfg.PC, status.Offline, "", ""); err != nil {
		a.log.Debug("offline report not sent", "err", err)
	}
}
