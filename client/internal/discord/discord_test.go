package discord

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeDiscord answers the RPC commands like the Discord app.
type fakeDiscord struct {
	t       *testing.T
	deny    bool // decline the consent window
	token   string
	mu      sync.Mutex
	conn    net.Conn
	authzed int // AUTHORIZE requests
}

func (f *fakeDiscord) dial(context.Context) (io.ReadWriteCloser, error) {
	client, server := net.Pipe()
	f.mu.Lock()
	f.conn = server
	f.mu.Unlock()
	go f.serve(server)
	return client, nil
}

func frame(op uint32, v any) []byte {
	body, _ := json.Marshal(v)
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, op)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(body)))
	return append(b, body...)
}

func (f *fakeDiscord) send(v any) {
	f.mu.Lock()
	c := f.conn
	f.mu.Unlock()
	c.Write(frame(opFrame, v))
}

func (f *fakeDiscord) serve(c net.Conn) {
	defer c.Close()
	for {
		var h [8]byte
		if _, err := io.ReadFull(c, h[:]); err != nil {
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(h[4:]))
		if _, err := io.ReadFull(c, body); err != nil {
			return
		}
		var req struct {
			Cmd   string         `json:"cmd"`
			Nonce string         `json:"nonce"`
			Args  map[string]any `json:"args"`
		}
		json.Unmarshal(body, &req)
		reply := func(data any) {
			c.Write(frame(opFrame, map[string]any{"cmd": req.Cmd, "nonce": req.Nonce, "data": data}))
		}
		fail := func(code int) {
			c.Write(frame(opFrame, map[string]any{"cmd": req.Cmd, "nonce": req.Nonce, "evt": "ERROR",
				"data": map[string]any{"code": code, "message": "no"}}))
		}
		switch {
		case binary.LittleEndian.Uint32(h[:]) == opHandshake:
			c.Write(frame(opFrame, map[string]any{"cmd": "DISPATCH", "evt": "READY", "data": map[string]any{"v": 1}}))
		case req.Cmd == "AUTHORIZE":
			f.mu.Lock()
			f.authzed++
			f.mu.Unlock()
			if f.deny {
				fail(5000)
			} else {
				reply(map[string]any{"code": "the-code"})
			}
		case req.Cmd == "AUTHENTICATE":
			if req.Args["access_token"] == f.token {
				reply(map[string]any{"user": map[string]any{"username": "me"}})
			} else {
				fail(4009)
			}
		case req.Cmd == "GET_VOICE_SETTINGS":
			reply(map[string]any{"mute": false, "deaf": false})
		case req.Cmd == "SUBSCRIBE":
			reply(map[string]any{"evt": "VOICE_SETTINGS_UPDATE"})
		}
	}
}

type memTokens struct {
	mu  sync.Mutex
	tok *Token
}

func (m *memTokens) Load() (Token, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tok == nil {
		return Token{}, false
	}
	return *m.tok, true
}
func (m *memTokens) Save(t Token) error { m.mu.Lock(); m.tok = &t; m.mu.Unlock(); return nil }
func (m *memTokens) Clear() error       { m.mu.Lock(); m.tok = nil; m.mu.Unlock(); return nil }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConsentThenFollows(t *testing.T) {
	f := &fakeDiscord{t: t, token: "access-1"}
	tokens := &memTokens{}
	var exchanged map[string]string
	c := New(Options{
		ClientID: "id", ClientSecret: "secret", Tokens: tokens, Dial: f.dial,
		Exchange: func(_ context.Context, form map[string]string) (Token, error) {
			exchanged = form
			return Token{Access: "access-1", Refresh: "r", Expiry: time.Now().Add(time.Hour)}, nil
		},
	})
	defer c.Close()
	c.SetEnabled(true)
	waitFor(t, "connected", func() bool { return c.State() == Connected })
	if exchanged["code"] != "the-code" || exchanged["grant_type"] != "authorization_code" {
		t.Fatalf("exchange %v", exchanged)
	}
	if m, known := c.Muted(); !known || m {
		t.Fatalf("muted %v known %v", m, known)
	}
	f.send(map[string]any{"cmd": "DISPATCH", "evt": "VOICE_SETTINGS_UPDATE", "data": map[string]any{"mute": true, "deaf": false}})
	waitFor(t, "muted", func() bool { m, _ := c.Muted(); return m })
	f.send(map[string]any{"cmd": "DISPATCH", "evt": "VOICE_SETTINGS_UPDATE", "data": map[string]any{"mute": false, "deaf": true}})
	waitFor(t, "deafened counts as muted", func() bool { m, _ := c.Muted(); return m })
	f.send(map[string]any{"cmd": "DISPATCH", "evt": "VOICE_SETTINGS_UPDATE", "data": map[string]any{"mute": false, "deaf": false}})
	waitFor(t, "unmuted", func() bool { m, _ := c.Muted(); return !m })

	// Turned off: nothing is known any more.
	c.SetEnabled(false)
	if _, known := c.Muted(); known || c.State() != Off {
		t.Fatal("still known after turning off")
	}

	// Turned on again: the saved token is used, no consent window.
	c.SetEnabled(true)
	waitFor(t, "connected again", func() bool { return c.State() == Connected })
	f.mu.Lock()
	asked := f.authzed
	f.mu.Unlock()
	if asked != 1 {
		t.Fatalf("consent asked %d times", asked)
	}
}

func TestDeclined(t *testing.T) {
	f := &fakeDiscord{t: t, deny: true}
	c := New(Options{ClientID: "id", ClientSecret: "secret", Tokens: &memTokens{}, Dial: f.dial,
		Exchange: func(context.Context, map[string]string) (Token, error) { return Token{}, errors.New("unused") }})
	defer c.Close()
	c.SetEnabled(true)
	waitFor(t, "declined", func() bool { return c.State() == Denied })
}

func TestNotRunningAndNoApp(t *testing.T) {
	c := New(Options{ClientID: "id", ClientSecret: "secret", Tokens: &memTokens{}, Retry: time.Hour,
		Dial: func(context.Context) (io.ReadWriteCloser, error) { return nil, errors.New("no pipe") }})
	c.SetEnabled(true)
	waitFor(t, "not running", func() bool { return c.State() == NotRunning })
	c.Close()

	none := New(Options{Tokens: &memTokens{}})
	none.SetEnabled(true)
	if none.State() != NoApp || none.HasApp() {
		t.Fatal("a build without an application should say so")
	}
}
