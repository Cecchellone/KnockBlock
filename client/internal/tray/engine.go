package tray

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/Cecchellone/KnockBlock/client/internal/agent"
	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
	"github.com/Cecchellone/KnockBlock/client/internal/status"
)

// Deps are the engine's collaborators, replaceable in tests.
type Deps struct {
	NewDetector func(detect.Options) (detect.Detector, error)
	Dial        func(espapi.Options) link.DialFunc
	OnResult    func(status.Result) // must not block
	OnState     func(link.State)    // must not block
	OnDoorbell  func()              // the light's doorbell rang; must not block
	// AppMuted is passed to the detector (detect.Options.AppMuted).
	AppMuted   func(exe string) (muted, known bool)
	Logger     *slog.Logger
	ClientInfo string
}

// Engine runs the presence loop (detector → agent → link) for one
// configuration. A new configuration with the same device address and key
// is applied without reconnecting; otherwise the loop restarts.
type Engine struct {
	deps Deps
	// doorbell is read by the connection's doorbell hook, so turning the
	// setting on or off needs no reconnection.
	doorbell atomic.Bool

	mu       sync.Mutex
	override status.Override
	run      *running
}

type running struct {
	address, key string
	pc           string
	det          detect.Detector
	sup          *link.Supervisor
	agent        *agent.Agent
	stopAgent    context.CancelFunc
	stopSup      context.CancelFunc
	agentDone    chan struct{}
	supervisor   chan struct{}
}

// NewEngine returns a stopped engine.
func NewEngine(deps Deps) *Engine {
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	return &Engine{deps: deps}
}

// Start starts the loop with cfg, or applies cfg to the running loop. Only
// a change of device address or key reconnects (the tray shows
// "connecting" meanwhile); the PC name, the detection settings and the
// doorbell setting change in place. A changed PC name is reported offline
// first, so it does not linger on the device.
func (e *Engine) Start(cfg config.Config) error {
	key, err := cfg.Key()
	if err != nil {
		return err
	}
	pc := cfg.PCName
	if pc == "" {
		pc, _ = os.Hostname()
	}
	det, err := e.deps.NewDetector(detect.Options{Ignore: cfg.Ignore, Names: cfg.AppNames, AppMuted: e.deps.AppMuted})
	if err != nil {
		return err
	}
	e.doorbell.Store(cfg.Doorbell)

	e.mu.Lock()
	if r := e.run; r != nil && r.address == cfg.Address() && r.key == key {
		r.det, r.pc = det, pc
		r.agent.Update(pc, cfg.SendAppNames, det)
		e.mu.Unlock()
		e.deps.Logger.Info("engine updated", "pc", pc)
		return nil
	}
	e.mu.Unlock()
	e.Stop()

	var onConnect func(context.Context, link.Conn, espapi.Entities) error
	if e.deps.OnDoorbell != nil {
		onConnect = link.WatchDoorbell(func() {
			on := e.doorbell.Load()
			e.deps.Logger.Info("doorbell event from the light", "notify", on)
			if on {
				e.deps.OnDoorbell()
			}
		})
	}
	sup := link.New(link.Options{
		Dial:      e.deps.Dial(espapi.Options{Address: cfg.Address(), EncryptionKey: key, ClientInfo: e.deps.ClientInfo, Logger: e.deps.Logger}),
		OnState:   e.deps.OnState,
		OnConnect: onConnect,
		Logger:    e.deps.Logger,
	})
	a := agent.New(agent.Config{
		PC: pc, SendApps: cfg.SendAppNames, Detector: det, Link: sup,
		OnResult: e.deps.OnResult, Logger: e.deps.Logger,
	})

	e.mu.Lock()
	defer e.mu.Unlock()
	a.SetOverride(e.override)
	r := &running{address: cfg.Address(), key: key, pc: pc, det: det, sup: sup, agent: a, agentDone: make(chan struct{}), supervisor: make(chan struct{})}
	supCtx, stopSup := context.WithCancel(context.Background())
	agentCtx, stopAgent := context.WithCancel(context.Background())
	r.stopSup, r.stopAgent = stopSup, stopAgent
	go func() { sup.Run(supCtx); close(r.supervisor) }()
	go func() { a.Run(agentCtx); close(r.agentDone) }()
	e.run = r
	e.deps.Logger.Info("engine started", "device", cfg.Address(), "pc", pc)
	return nil
}

// Stop stops the loop: the agent first, so its offline report still goes
// out, then the connection.
func (e *Engine) Stop() {
	e.mu.Lock()
	r := e.run
	e.run = nil
	e.mu.Unlock()
	if r == nil {
		return
	}
	r.stopAgent()
	<-r.agentDone
	r.stopSup()
	<-r.supervisor
	r.det.Close()
}

// SetOverride sets the manual override; it survives restarts.
func (e *Engine) SetOverride(o status.Override) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.override = o
	if e.run != nil {
		e.run.agent.SetOverride(o)
	}
}

// Override returns the current manual override.
func (e *Engine) Override() status.Override {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.override
}

// CanShowMessage reports whether the light is connected and can show a
// message from this PC.
func (e *Engine) CanShowMessage() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run != nil && e.run.sup.CanShowMessage()
}

// ShowMessage shows a message from this PC on the light's display.
func (e *Engine) ShowMessage(ctx context.Context, text string) error {
	e.mu.Lock()
	r := e.run
	var pc string
	if r != nil {
		pc = r.pc
	}
	e.mu.Unlock()
	if r == nil {
		return link.ErrNotConnected
	}
	return r.sup.ShowMessage(ctx, pc, text)
}

// ForceReconnect reconnects at once (resume from sleep, network change).
func (e *Engine) ForceReconnect() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.run != nil {
		e.run.sup.ForceReconnect()
	}
}
