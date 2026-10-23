package tray

import (
	"context"
	"log/slog"
	"os"
	"sync"

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
	Logger      *slog.Logger
	ClientInfo  string
}

// Engine runs the presence loop (detector → agent → link) for one
// configuration, and restarts it when the configuration changes.
type Engine struct {
	deps Deps

	mu       sync.Mutex
	override status.Override
	run      *running
}

type running struct {
	det        detect.Detector
	sup        *link.Supervisor
	agent      *agent.Agent
	stopAgent  context.CancelFunc
	stopSup    context.CancelFunc
	agentDone  chan struct{}
	supervisor chan struct{}
}

// NewEngine returns a stopped engine.
func NewEngine(deps Deps) *Engine {
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	return &Engine{deps: deps}
}

// Start (re)starts the loop with cfg. A running loop is stopped first,
// reporting offline, so a changed PC name does not linger on the device.
func (e *Engine) Start(cfg config.Config) error {
	e.Stop()
	key, err := cfg.Key()
	if err != nil {
		return err
	}
	pc := cfg.PCName
	if pc == "" {
		pc, _ = os.Hostname()
	}
	det, err := e.deps.NewDetector(detect.Options{Ignore: cfg.Ignore, Names: cfg.AppNames})
	if err != nil {
		return err
	}
	sup := link.New(link.Options{
		Dial:      e.deps.Dial(espapi.Options{Address: cfg.Address(), EncryptionKey: key, ClientInfo: e.deps.ClientInfo, Logger: e.deps.Logger}),
		OnState:   e.deps.OnState,
		OnConnect: link.SetDisplayLanguage(cfg.DisplayLanguage),
		Logger:    e.deps.Logger,
	})
	a := agent.New(agent.Config{
		PC: pc, SendApps: cfg.SendAppNames, Detector: det, Link: sup,
		OnResult: e.deps.OnResult, Logger: e.deps.Logger,
	})

	e.mu.Lock()
	defer e.mu.Unlock()
	a.SetOverride(e.override)
	r := &running{det: det, sup: sup, agent: a, agentDone: make(chan struct{}), supervisor: make(chan struct{})}
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

// ForceReconnect reconnects at once (resume from sleep, network change).
func (e *Engine) ForceReconnect() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.run != nil {
		e.run.sup.ForceReconnect()
	}
}
