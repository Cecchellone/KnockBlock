// Package discord reads Discord's mute and deafen buttons through Discord's
// local RPC interface, the one Stream Deck and Logitech G HUB use: a named
// pipe to the Discord app running on this PC. Discord keeps recording while
// muted, so Windows cannot tell; only Discord knows.
//
// Reading voice settings needs an OAuth2 application registered on
// Discord's developer portal (client ID and secret, built into release
// builds). The first time, Discord shows its own consent window; the token
// it grants is kept, protected for the Windows user, and refreshed. Until
// Discord approves the application, only its owner and up to 50 testers can
// authorize it (docs/discord.md).
//
// Protocol: frames of a little-endian opcode and length, then JSON; a
// handshake, then commands with a nonce answered by the same nonce, and
// events (https://discord.com/developers/docs/topics/rpc).
package discord

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// State is what the integration is doing, for the settings window.
type State int

// States.
const (
	Off        State = iota // turned off in the settings
	NoApp                   // this build has no Discord application
	NotRunning              // Discord is not running (or not reachable)
	Consent                 // waiting for the user's OK in Discord
	Denied                  // the user declined in Discord; turn off and on to ask again
	Connected               // mute and deafen are followed
	Failed                  // anything else; retried
)

// Key returns the locale key describing the state.
func (s State) Key() string {
	return [...]string{"discord.off", "discord.no_app", "discord.not_running", "discord.consent",
		"discord.denied", "discord.connected", "discord.failed"}[s]
}

// Options configures a Client.
type Options struct {
	ClientID, ClientSecret string
	// Tokens loads and saves the OAuth2 tokens (protected by the caller).
	Tokens TokenStore
	// Dial opens the RPC pipe; default: the Windows named pipes.
	Dial func(ctx context.Context) (io.ReadWriteCloser, error)
	// Exchange trades an authorization code or a refresh token for tokens;
	// default: Discord's OAuth2 token endpoint.
	Exchange func(ctx context.Context, form map[string]string) (Token, error)
	Logger   *slog.Logger
	// Retry is the wait between attempts while Discord is not running
	// (default 10 s).
	Retry time.Duration
}

// Token is an OAuth2 token pair.
type Token struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	Expiry  time.Time `json:"expiry"`
}

// TokenStore keeps the tokens between runs.
type TokenStore interface {
	Load() (Token, bool)
	Save(Token) error
	Clear() error
}

// Client follows Discord's mute and deafen buttons while enabled. Its
// methods are safe for concurrent use.
type Client struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	state   State
	muted   bool // mute or deafen on
	known   bool
	cancel  context.CancelFunc
	done    chan struct{}
	enabled bool
}

// New returns a stopped client.
func New(opts Options) *Client {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Dial == nil {
		opts.Dial = dialPipe
	}
	if opts.Exchange == nil {
		opts.Exchange = func(ctx context.Context, form map[string]string) (Token, error) {
			return exchange(ctx, opts.ClientID, opts.ClientSecret, form)
		}
	}
	if opts.Retry <= 0 {
		opts.Retry = 10 * time.Second
	}
	c := &Client{opts: opts, log: opts.Logger}
	if opts.ClientID == "" || opts.ClientSecret == "" {
		c.state = NoApp
	}
	return c
}

// HasApp reports whether this build can use Discord at all.
func (c *Client) HasApp() bool { return c.opts.ClientID != "" && c.opts.ClientSecret != "" }

// SetEnabled starts or stops following Discord. Turning it on again after
// the user declined asks again.
func (c *Client) SetEnabled(on bool) {
	c.mu.Lock()
	if on == c.enabled || !c.HasApp() {
		c.mu.Unlock()
		return
	}
	c.enabled = on
	if !on {
		cancel, done := c.cancel, c.done
		c.cancel, c.done = nil, nil
		c.state, c.known = Off, false
		c.mu.Unlock()
		cancel()
		<-done
		return
	}
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
	if !c.enabled && c.state != NoApp {
		return Off
	}
	return c.state
}

// Muted reports whether Discord's mute or deafen button is on, and whether
// that is known (connected to Discord).
func (c *Client) Muted() (muted, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.muted, c.known
}

func (c *Client) set(st State) {
	c.mu.Lock()
	c.state = st
	if st != Connected {
		c.known = false
	}
	c.mu.Unlock()
}

func (c *Client) voice(mute, deaf bool) {
	c.mu.Lock()
	c.muted, c.known = mute || deaf, true
	c.mu.Unlock()
}

// errDenied means the user declined the consent window.
var errDenied = errors.New("discord: authorization declined")

func (c *Client) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errDenied) {
			c.log.Info("discord: authorization declined")
			c.set(Denied)
			<-ctx.Done() // until turned off and on again
			return
		}
		c.log.Debug("discord: session ended", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.opts.Retry):
		}
	}
}

// session connects, authorizes if needed, and follows the voice settings
// until the pipe closes.
func (c *Client) session(ctx context.Context) error {
	pipe, err := c.opts.Dial(ctx)
	if err != nil {
		c.set(NotRunning)
		return err
	}
	stop := context.AfterFunc(ctx, func() { pipe.Close() })
	defer stop()
	defer pipe.Close()
	s := &session{rw: pipe, voice: c.voice}

	if err := s.handshake(c.opts.ClientID); err != nil {
		c.set(NotRunning)
		return err
	}
	tok, ok := c.opts.Tokens.Load()
	if ok && time.Until(tok.Expiry) < time.Minute && tok.Refresh != "" {
		if t, err := c.opts.Exchange(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": tok.Refresh}); err == nil {
			tok = t
			c.opts.Tokens.Save(tok)
		} else {
			ok = false
		}
	}
	if !ok || s.authenticate(tok.Access) != nil {
		c.set(Consent)
		code, err := s.authorize(c.opts.ClientID)
		if err != nil {
			return err
		}
		tok, err = c.opts.Exchange(ctx, map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": RedirectURI})
		if err != nil {
			c.set(Failed)
			return err
		}
		if err := c.opts.Tokens.Save(tok); err != nil {
			c.log.Warn("discord: saving the token", "err", err)
		}
		if err := s.authenticate(tok.Access); err != nil {
			c.opts.Tokens.Clear()
			c.set(Failed)
			return err
		}
	}
	if err := s.subscribe(); err != nil {
		c.set(Failed)
		return err
	}
	c.set(Connected)
	c.log.Info("discord: connected")
	return s.follow()
}

// RedirectURI is the redirect registered for the Discord application; the
// RPC authorization never opens it, but the token exchange names it.
const RedirectURI = "http://localhost"

// Opcodes.
const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
	opPing      = 3
	opPong      = 4
)

// maxFrame bounds a frame; Discord's are a few kilobytes.
const maxFrame = 1 << 20

type session struct {
	rw    io.ReadWriter
	nonce int
	voice func(mute, deaf bool)
}

type message struct {
	Cmd   string          `json:"cmd"`
	Evt   string          `json:"evt"`
	Nonce string          `json:"nonce"`
	Data  json.RawMessage `json:"data"`
}

type voiceSettings struct {
	Mute *bool `json:"mute"`
	Deaf *bool `json:"deaf"`
}

func (s *session) write(op uint32, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	frame := make([]byte, 8, 8+len(body))
	binary.LittleEndian.PutUint32(frame, op)
	binary.LittleEndian.PutUint32(frame[4:], uint32(len(body)))
	_, err = s.rw.Write(append(frame, body...))
	return err
}

// read returns the next frame's message, answering pings.
func (s *session) read() (message, error) {
	for {
		var h [8]byte
		if _, err := io.ReadFull(s.rw, h[:]); err != nil {
			return message{}, err
		}
		op, n := binary.LittleEndian.Uint32(h[:]), binary.LittleEndian.Uint32(h[4:])
		if n > maxFrame {
			return message{}, fmt.Errorf("discord: frame of %d bytes", n)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(s.rw, body); err != nil {
			return message{}, err
		}
		switch op {
		case opPing:
			if err := s.writeRaw(opPong, body); err != nil {
				return message{}, err
			}
			continue
		case opClose:
			return message{}, fmt.Errorf("discord: closed: %s", body)
		case opFrame, opHandshake:
		default:
			continue
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			return message{}, fmt.Errorf("discord: bad frame: %w", err)
		}
		if m.Evt == "VOICE_SETTINGS_UPDATE" {
			s.applyVoice(m.Data)
		}
		return m, nil
	}
}

func (s *session) writeRaw(op uint32, body []byte) error {
	frame := make([]byte, 8, 8+len(body))
	binary.LittleEndian.PutUint32(frame, op)
	binary.LittleEndian.PutUint32(frame[4:], uint32(len(body)))
	_, err := s.rw.Write(append(frame, body...))
	return err
}

func (s *session) applyVoice(data json.RawMessage) {
	var v voiceSettings
	if json.Unmarshal(data, &v) == nil && v.Mute != nil && v.Deaf != nil {
		s.voice(*v.Mute, *v.Deaf)
	}
}

func (s *session) handshake(clientID string) error {
	if err := s.write(opHandshake, map[string]any{"v": 1, "client_id": clientID}); err != nil {
		return err
	}
	for {
		m, err := s.read()
		if err != nil {
			return err
		}
		if m.Evt == "READY" {
			return nil
		}
	}
}

// command sends a command and waits for its answer (events in between are
// applied); an ERROR answer becomes an error.
func (s *session) command(cmd string, args any, evt string) (json.RawMessage, error) {
	s.nonce++
	nonce := strconv.Itoa(s.nonce)
	req := map[string]any{"cmd": cmd, "nonce": nonce}
	if args != nil {
		req["args"] = args
	}
	if evt != "" {
		req["evt"] = evt
	}
	if err := s.write(opFrame, req); err != nil {
		return nil, err
	}
	for {
		m, err := s.read()
		if err != nil {
			return nil, err
		}
		if m.Nonce != nonce {
			continue
		}
		if m.Evt == "ERROR" {
			var e struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			json.Unmarshal(m.Data, &e)
			return nil, &rpcError{cmd: cmd, code: e.Code, msg: e.Message}
		}
		return m.Data, nil
	}
}

type rpcError struct {
	cmd  string
	code int
	msg  string
}

func (e *rpcError) Error() string { return fmt.Sprintf("discord: %s: %d %s", e.cmd, e.code, e.msg) }

// authorize shows Discord's consent window and returns the code granted.
func (s *session) authorize(clientID string) (string, error) {
	data, err := s.command("AUTHORIZE", map[string]any{
		"client_id": clientID,
		"scopes":    []string{"rpc", "rpc.voice.read"},
	}, "")
	if err != nil {
		var re *rpcError
		if errors.As(err, &re) && (re.code == 5000 || re.code == 4002) {
			// 5000: OAuth2 error (declined), 4002: user cancelled.
			return "", fmt.Errorf("%w: %v", errDenied, err)
		}
		return "", err
	}
	var v struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &v); err != nil || v.Code == "" {
		return "", fmt.Errorf("discord: AUTHORIZE without a code")
	}
	return v.Code, nil
}

func (s *session) authenticate(access string) error {
	if access == "" {
		return errors.New("discord: no token")
	}
	_, err := s.command("AUTHENTICATE", map[string]any{"access_token": access}, "")
	return err
}

// subscribe reads the voice settings once and asks for their changes.
func (s *session) subscribe() error {
	data, err := s.command("GET_VOICE_SETTINGS", nil, "")
	if err != nil {
		return err
	}
	s.applyVoice(data)
	_, err = s.command("SUBSCRIBE", nil, "VOICE_SETTINGS_UPDATE")
	return err
}

// follow applies voice setting events until the pipe closes.
func (s *session) follow() error {
	for {
		if _, err := s.read(); err != nil {
			return err
		}
	}
}
