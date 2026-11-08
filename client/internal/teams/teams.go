// Package teams reads Microsoft Teams' mute button through the "third-party
// app API" of the new Teams: a WebSocket on 127.0.0.1:8124 that Stream Deck
// and similar devices use. Teams keeps recording while muted, so Windows
// cannot tell; only Teams knows.
//
// The user turns the API on once in Teams (Settings → Privacy → Manage API
// → Enable API). The first connection has no token: during a meeting,
// any command makes Teams ask the user to allow KnockBlock, and Teams then
// sends a token, kept (protected by the caller) for the next connections.
//
// Protocol (protocol-version 2.0.0), as used by community clients such as
// github.com/ferenyl/Teams.ThirdPartyAppApi (TeamsClient.cs): the URL
// carries token, protocol-version, manufacturer, device, app and
// app-version; Teams sends {"meetingUpdate":{"meetingState":{...},
// "meetingPermissions":{...}}}, {"tokenRefresh":"..."}, and answers
// commands {"action":"...","parameters":{},"requestId":n} with
// {"requestId":n,"response":"Success"} or an errorMsg.
package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"time"
)

// State is what the integration is doing, for the settings window.
type State int

// States.
const (
	Off        State = iota // turned off in the settings
	NotRunning              // Teams is not running, or its API is off
	Pairing                 // connected; the user must allow KnockBlock during a meeting
	Connected               // paired: the mute button is followed
)

// Key returns the locale key describing the state.
func (s State) Key() string {
	return [...]string{"teams.off", "teams.not_running", "teams.pairing", "teams.connected"}[s]
}

// TokenStore keeps Teams' token between runs.
type TokenStore interface {
	Load() (string, bool)
	Save(string) error
}

// Options configures a Client.
type Options struct {
	Tokens  TokenStore
	Version string // KnockBlock's version, sent to Teams
	// Address of Teams' API (default 127.0.0.1:8124).
	Address string
	Logger  *slog.Logger
	// Retry is the wait between attempts while Teams is not reachable
	// (default 10 s).
	Retry time.Duration
}

// Client follows the Teams mute button while enabled. Safe for concurrent
// use.
type Client struct {
	opts Options
	log  *slog.Logger

	mu        sync.Mutex
	enabled   bool
	state     State
	inMeeting bool
	muted     bool
	cancel    context.CancelFunc
	done      chan struct{}
}

// New returns a stopped client.
func New(opts Options) *Client {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Address == "" {
		opts.Address = "127.0.0.1:8124"
	}
	if opts.Retry <= 0 {
		opts.Retry = 10 * time.Second
	}
	return &Client{opts: opts, log: opts.Logger}
}

// SetEnabled starts or stops following Teams.
func (c *Client) SetEnabled(on bool) {
	c.mu.Lock()
	if on == c.enabled {
		c.mu.Unlock()
		return
	}
	c.enabled = on
	if !on {
		cancel, done := c.cancel, c.done
		c.cancel, c.done = nil, nil
		c.state, c.inMeeting = Off, false
		c.mu.Unlock()
		cancel()
		<-done
		return
	}
	c.state = NotRunning // until the first connection
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel, c.done = cancel, make(chan struct{})
	done := c.done
	c.mu.Unlock()
	go func() {
		defer close(done)
		c.run(ctx)
	}()
}

// Close stops the client.
func (c *Client) Close() { c.SetEnabled(false) }

// State returns what the integration is doing.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return Off
	}
	return c.state
}

// Muted reports whether the Teams mute button is on during a meeting;
// known is false outside meetings and while not paired.
func (c *Client) Muted() (muted, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.muted, c.state == Connected && c.inMeeting
}

func (c *Client) set(st State) {
	c.mu.Lock()
	c.state = st
	if st != Connected {
		c.inMeeting = false
	}
	c.mu.Unlock()
}

func (c *Client) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.set(NotRunning)
		c.log.Debug("teams: session ended", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.opts.Retry):
		}
	}
}

type serverMessage struct {
	RequestID     int    `json:"requestId"`
	Response      string `json:"response"`
	ErrorMsg      string `json:"errorMsg"`
	TokenRefresh  string `json:"tokenRefresh"`
	MeetingUpdate *struct {
		MeetingState struct {
			IsMuted     bool `json:"isMuted"`
			IsInMeeting bool `json:"isInMeeting"`
		} `json:"meetingState"`
		MeetingPermissions struct {
			CanPair bool `json:"canPair"`
		} `json:"meetingPermissions"`
	} `json:"meetingUpdate"`
}

func (c *Client) session(ctx context.Context) error {
	token, paired := c.opts.Tokens.Load()
	q := url.Values{
		"protocol-version": {"2.0.0"},
		"manufacturer":     {"KnockBlock"},
		"device":           {"KnockBlock"},
		"app":              {"KnockBlock"},
		"app-version":      {c.opts.Version},
	}
	if paired {
		q.Set("token", token)
	}
	u := &url.URL{Scheme: "ws", Host: c.opts.Address, Path: "/", RawQuery: q.Encode()}
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := d.DialContext(dctx, "tcp", c.opts.Address)
	cancel()
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	ws, err := wsDial(conn, u)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Time{})
	if paired {
		c.set(Connected)
		c.log.Info("teams: connected")
	} else {
		c.set(Pairing)
	}
	requestID := 0
	asked := false
	for {
		raw, err := ws.readMessage()
		if err != nil {
			return err
		}
		var m serverMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m.TokenRefresh != "" {
			if err := c.opts.Tokens.Save(m.TokenRefresh); err != nil {
				c.log.Warn("teams: saving the token", "err", err)
			}
			paired = true
			c.set(Connected)
			c.log.Info("teams: paired")
			continue
		}
		if m.ErrorMsg != "" {
			c.log.Debug("teams: error", "msg", m.ErrorMsg)
			continue
		}
		mu := m.MeetingUpdate
		if mu == nil {
			continue
		}
		c.mu.Lock()
		c.inMeeting, c.muted = mu.MeetingState.IsInMeeting, mu.MeetingState.IsMuted
		c.mu.Unlock()
		// Not paired yet: during a meeting a command makes Teams ask the
		// user to allow KnockBlock. query-state changes nothing in the call.
		// TODO(verify): that query-state is enough to start the pairing
		// prompt; community clients say any command is.
		if !paired && mu.MeetingPermissions.CanPair && !asked {
			asked = true
			requestID++
			req, _ := json.Marshal(map[string]any{"action": "query-state", "parameters": map[string]any{}, "requestId": requestID})
			if err := ws.writeText(req); err != nil {
				return fmt.Errorf("teams: asking to pair: %w", err)
			}
		}
	}
}
