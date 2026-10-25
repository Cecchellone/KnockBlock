package teams

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTeams is a WebSocket server answering like Teams' local API.
type fakeTeams struct {
	t      *testing.T
	ln     net.Listener
	mu     sync.Mutex
	conn   *wsConn
	tokens []string // token query parameter of each connection
	got    chan map[string]any
}

func newFakeTeams(t *testing.T) *fakeTeams {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTeams{t: t, ln: ln, got: make(chan map[string]any, 10)}
	go f.accept()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeTeams) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			c.Close()
			continue
		}
		sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(sum[:])+"\r\n\r\n")
		ws := &wsConn{conn: c, br: br}
		f.mu.Lock()
		f.conn = ws
		f.tokens = append(f.tokens, req.URL.Query().Get("token"))
		f.mu.Unlock()
		go func() {
			for {
				// Client frames are masked; readMessage unmasks them.
				msg, err := ws.readMessage()
				if err != nil {
					return
				}
				var m map[string]any
				json.Unmarshal(msg, &m)
				f.got <- m
			}
		}()
	}
}

// send writes an unmasked server frame.
func (f *fakeTeams) send(v any) {
	f.mu.Lock()
	ws := f.conn
	f.mu.Unlock()
	b, _ := json.Marshal(v)
	frame := append([]byte{0x81, byte(len(b))}, b...)
	if len(b) >= 126 {
		frame = append([]byte{0x81, 126, byte(len(b) >> 8), byte(len(b))}, b...)
	}
	ws.conn.Write(frame)
}

func meeting(inMeeting, muted, canPair bool) map[string]any {
	return map[string]any{"meetingUpdate": map[string]any{
		"meetingState":       map[string]any{"isMuted": muted, "isInMeeting": inMeeting},
		"meetingPermissions": map[string]any{"canPair": canPair},
	}}
}

type memToken struct {
	mu  sync.Mutex
	tok string
}

func (m *memToken) Load() (string, bool) { m.mu.Lock(); defer m.mu.Unlock(); return m.tok, m.tok != "" }
func (m *memToken) Save(t string) error  { m.mu.Lock(); m.tok = t; m.mu.Unlock(); return nil }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPairThenFollow(t *testing.T) {
	f := newFakeTeams(t)
	tok := &memToken{}
	c := New(Options{Tokens: tok, Address: f.ln.Addr().String(), Version: "1.0", Retry: 50 * time.Millisecond})
	defer c.Close()
	c.SetEnabled(true)
	waitFor(t, "pairing", func() bool { return c.State() == Pairing })

	// In a meeting, Teams allows pairing: KnockBlock sends a harmless
	// command to start it.
	f.send(meeting(true, false, true))
	select {
	case m := <-f.got:
		if m["action"] != "query-state" {
			t.Fatalf("pairing command %v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no pairing command")
	}
	if _, known := c.Muted(); known {
		t.Fatal("known before pairing")
	}
	f.send(map[string]any{"tokenRefresh": "tok-1"})
	waitFor(t, "connected", func() bool { return c.State() == Connected })
	if tok.tok != "tok-1" {
		t.Fatalf("token %q not saved", tok.tok)
	}
	f.send(meeting(true, true, false))
	waitFor(t, "muted", func() bool { m, known := c.Muted(); return m && known })
	f.send(meeting(false, false, false))
	waitFor(t, "out of the meeting", func() bool { _, known := c.Muted(); return !known })

	// Reconnecting uses the saved token.
	c.SetEnabled(false)
	c.SetEnabled(true)
	waitFor(t, "connected again", func() bool { return c.State() == Connected })
	f.mu.Lock()
	defer f.mu.Unlock()
	if last := f.tokens[len(f.tokens)-1]; last != "tok-1" {
		t.Fatalf("second connection token %q", last)
	}
}

func TestNotRunning(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listens there
	c := New(Options{Tokens: &memToken{}, Address: addr, Retry: time.Hour})
	defer c.Close()
	c.SetEnabled(true)
	waitFor(t, "not running", func() bool { return c.State() == NotRunning })
	if !strings.Contains(c.State().Key(), "not_running") {
		t.Fatal(c.State().Key())
	}
}
