package espapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi/pb"
)

// DefaultPort is the Native API TCP port.
const DefaultPort = "6053"

// API version this client implements, the one the pinned ESPHome release
// (proto/VERSION) reports and aioesphomeapi sends for it
// (aioesphomeapi/connection.py, make_hello_request).
const (
	apiVersionMajor = 1
	apiVersionMinor = 19
)

// Timeouts and keepalive, taken from aioesphomeapi/connection.py.
const (
	defaultKeepAlive = 20 * time.Second // KEEP_ALIVE_FREQUENCY
	// The connection is dead after this many keepalive intervals without
	// any traffic (KEEP_ALIVE_TIMEOUT_RATIO).
	keepAliveTimeoutRatio = 4.5
	helloTimeout          = 30 * time.Second // CONNECT_REQUEST_TIMEOUT
	disconnectTimeout     = 10 * time.Second // DISCONNECT_RESPONSE_TIMEOUT
	writeTimeout          = 10 * time.Second
)

// Options configures Dial.
type Options struct {
	// Address is "host:port" or just "host"; the port defaults to 6053.
	Address string
	// EncryptionKey is the device's base64 Noise key (api: encryption: key:).
	// Empty selects the plaintext transport. The key is never logged.
	EncryptionKey string
	// ClientInfo identifies this client to the device, like a User-Agent.
	ClientInfo string
	// ExpectedName, if set, must equal the name the device reports.
	ExpectedName string
	// KeepAlive is the ping interval (default 20 s). Without any traffic for
	// 4.5 times this, the connection is closed with ErrTimeout.
	KeepAlive time.Duration
	// Logger receives debug logs; nil discards them.
	Logger *slog.Logger
}

// Conn is a connection to one ESPHome device. Its methods are safe for
// concurrent use. One goroutine reads from the device; writes are
// serialized.
type Conn struct {
	nc        net.Conn
	tr        transport
	log       *slog.Logger
	keepAlive time.Duration
	start     time.Time    // base for lastRecv, so it uses the monotonic clock
	lastRecv  atomic.Int64 // nanoseconds since start of the last received frame

	deviceName string
	serverInfo string
	mac        string // from the Noise ServerHello; empty for plaintext
	apiMajor   uint32
	apiMinor   uint32

	writeMu sync.Mutex
	mbuf    []byte // reused for marshaling, guarded by writeMu

	mu            sync.Mutex
	listing       *listing
	onState       func(State)
	disconnectAck chan struct{}

	decoders map[uint32]proto.Message // reused per message type, reader only

	closeOnce sync.Once
	done      chan struct{}
	err       error // valid once done is closed
}

type listing struct {
	entities Entities
	done     chan struct{}
}

// Dial connects to a device, performs the Hello exchange and starts the
// reader and keepalive goroutines. The context bounds connection setup
// only.
func Dial(ctx context.Context, opts Options) (*Conn, error) {
	addr := opts.Address
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, DefaultPort)
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("espapi: connect to %s: %w", addr, err)
	}
	c := &Conn{
		nc:        nc,
		log:       opts.Logger,
		keepAlive: opts.KeepAlive,
		start:     time.Now(),
		decoders:  make(map[uint32]proto.Message),
		done:      make(chan struct{}),
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.keepAlive <= 0 {
		c.keepAlive = defaultKeepAlive
	}
	if err := c.setup(ctx, opts); err != nil {
		nc.Close()
		return nil, err
	}
	go c.readLoop()
	go c.keepAliveLoop()
	return c, nil
}

// setup selects the transport and performs the Hello exchange, under a
// deadline and aborted when ctx is cancelled.
func (c *Conn) setup(ctx context.Context, opts Options) error {
	deadline := time.Now().Add(helloTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.nc.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { c.nc.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	err := c.handshake(opts)
	if err == nil {
		err = c.hello(opts)
	}
	if err != nil {
		var ne net.Error
		timedOut := errors.As(err, &ne) && ne.Timeout()
		// The socket deadline may fire just before ctx reports its own.
		if ctxDeadline, ok := ctx.Deadline(); ctx.Err() != nil || (timedOut && ok && !time.Now().Before(ctxDeadline)) {
			cause := ctx.Err()
			if cause == nil {
				cause = context.DeadlineExceeded
			}
			return fmt.Errorf("espapi: connection setup: %w", cause)
		}
		if timedOut {
			return fmt.Errorf("%w: connection setup: %v", ErrTimeout, err)
		}
		return err
	}
	c.nc.SetDeadline(time.Time{})
	return nil
}

// handshake sets up the transport: plaintext without a key, otherwise the
// Noise handshake.
func (c *Conn) handshake(opts Options) error {
	br := bufio.NewReaderSize(c.nc, 1024)
	if opts.EncryptionKey == "" {
		c.tr = newPlaintextTransport(br, c.nc)
		return nil
	}
	psk, err := decodeNoiseKey(opts.EncryptionKey)
	if err != nil {
		return err
	}
	tr, hello, err := noiseClientHandshake(br, c.nc, psk, opts.ExpectedName, nil)
	if err != nil {
		return err
	}
	c.tr = tr
	c.mac = hello.mac
	return nil
}

// hello sends HelloRequest and waits for HelloResponse
// (aioesphomeapi/connection.py, _connect_hello_login). Password
// authentication was removed in ESPHome 2026.1.0.
func (c *Conn) hello(opts Options) error {
	if err := c.writeMessage(&pb.HelloRequest{
		ClientInfo:      opts.ClientInfo,
		ApiVersionMajor: apiVersionMajor,
		ApiVersionMinor: apiVersionMinor,
	}); err != nil {
		return err
	}
	helloID := messageID((*pb.HelloResponse)(nil))
	for {
		id, payload, err := c.tr.readFrame()
		if err != nil {
			return err
		}
		if id != helloID {
			continue
		}
		var resp pb.HelloResponse
		if err := proto.Unmarshal(payload, &resp); err != nil {
			return fmt.Errorf("%w: decoding HelloResponse: %v", ErrProtocol, err)
		}
		// aioesphomeapi accepts any major version up to 2 (_process_hello_resp).
		if resp.GetApiVersionMajor() > 2 {
			return fmt.Errorf("%w: device speaks API %d.%d", ErrIncompatibleAPI,
				resp.GetApiVersionMajor(), resp.GetApiVersionMinor())
		}
		if opts.ExpectedName != "" && resp.GetName() != opts.ExpectedName {
			return fmt.Errorf("%w: expected %q, device is %q", ErrBadName, opts.ExpectedName, resp.GetName())
		}
		c.deviceName = resp.GetName()
		c.serverInfo = resp.GetServerInfo()
		c.apiMajor = resp.GetApiVersionMajor()
		c.apiMinor = resp.GetApiVersionMinor()
		c.touch()
		return nil
	}
}

// DeviceName is the device name reported in HelloResponse.
func (c *Conn) DeviceName() string { return c.deviceName }

// MACAddress is the device MAC address announced during the Noise handshake,
// or empty for plaintext connections.
func (c *Conn) MACAddress() string { return c.mac }

// ServerInfo describes the device firmware, e.g. its ESPHome version.
func (c *Conn) ServerInfo() string { return c.serverInfo }

// APIVersion is the API version reported by the device.
func (c *Conn) APIVersion() (major, minor uint32) { return c.apiMajor, c.apiMinor }

// Done is closed when the connection is closed; Err then says why.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection closed, or nil while it is open.
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// ListEntities returns the select, button and text sensor entities of the
// device. Other entity types are skipped.
func (c *Conn) ListEntities(ctx context.Context) (Entities, error) {
	l := &listing{done: make(chan struct{})}
	c.mu.Lock()
	if c.listing != nil {
		c.mu.Unlock()
		return nil, errors.New("espapi: ListEntities already in progress")
	}
	c.listing = l
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.listing == l {
			c.listing = nil
		}
		c.mu.Unlock()
	}()

	if err := c.send(ctx, &pb.ListEntitiesRequest{}); err != nil {
		return nil, err
	}
	select {
	case <-l.done:
		return l.entities, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err
	}
}

// SubscribeStates asks the device to send the state of every entity now and
// on every change. fn is called from the reader goroutine for select and
// text sensor states, also when another client (Home Assistant) changes
// them; it must not block.
func (c *Conn) SubscribeStates(ctx context.Context, fn func(State)) error {
	c.mu.Lock()
	c.onState = fn
	c.mu.Unlock()
	return c.send(ctx, &pb.SubscribeStatesRequest{})
}

// SetSelect sets a select entity to option.
func (c *Conn) SetSelect(ctx context.Context, key uint32, option string) error {
	return c.send(ctx, &pb.SelectCommandRequest{Key: key, State: option})
}

// PressButton presses a button entity.
func (c *Conn) PressButton(ctx context.Context, key uint32) error {
	return c.send(ctx, &pb.ButtonCommandRequest{Key: key})
}

// Close disconnects gracefully: it sends DisconnectRequest and waits briefly
// for DisconnectResponse before closing the socket.
func (c *Conn) Close() error {
	select {
	case <-c.done:
		return nil
	default:
	}
	ack := make(chan struct{})
	c.mu.Lock()
	c.disconnectAck = ack
	c.mu.Unlock()
	if err := c.send(context.Background(), &pb.DisconnectRequest{}); err == nil {
		timer := time.NewTimer(disconnectTimeout)
		select {
		case <-ack:
		case <-c.done:
		case <-timer.C:
		}
		timer.Stop()
	}
	c.fail(ErrClosed)
	return nil
}

// send writes one message unless the connection is closed or ctx is done.
func (c *Conn) send(ctx context.Context, m proto.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return c.err
	default:
	}
	c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := c.writeMessage(m); err != nil {
		c.fail(fmt.Errorf("espapi: write: %w", err))
		return c.Err()
	}
	return nil
}

// writeMessage marshals and writes one message.
func (c *Conn) writeMessage(m proto.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	b, err := proto.MarshalOptions{}.MarshalAppend(c.mbuf[:0], m)
	if err != nil {
		return fmt.Errorf("espapi: marshal %s: %w", m.ProtoReflect().Descriptor().Name(), err)
	}
	c.mbuf = b
	return c.tr.writeFrame(messageID(m), b)
}

func (c *Conn) readLoop() {
	table := messages()
	for {
		id, payload, err := c.tr.readFrame()
		if err != nil {
			c.fail(err)
			return
		}
		c.touch()
		mt, ok := table.byID[id]
		if !ok {
			c.log.Debug("skipping unknown message type", "type", id)
			continue
		}
		msg := c.decoders[id]
		if msg == nil {
			msg = mt.New().Interface()
			c.decoders[id] = msg
		}
		if err := proto.Unmarshal(payload, msg); err != nil {
			c.fail(fmt.Errorf("%w: decoding %s: %v", ErrProtocol, mt.Descriptor().Name(), err))
			return
		}
		c.handle(msg)
	}
}

// handle processes one decoded message on the reader goroutine.
func (c *Conn) handle(msg proto.Message) {
	switch m := msg.(type) {
	case *pb.PingRequest:
		if err := c.send(context.Background(), &pb.PingResponse{}); err != nil {
			c.log.Debug("ping response failed", "err", err)
		}
	case *pb.PingResponse:
		// Any traffic counts as liveness; nothing else to do.
	case *pb.DisconnectRequest:
		// Acknowledge, then close (aioesphomeapi/connection.py,
		// _handle_disconnect_request_internal).
		c.send(context.Background(), &pb.DisconnectResponse{})
		c.fail(fmt.Errorf("%w (reason %v)", ErrDisconnected, m.GetReason()))
	case *pb.DisconnectResponse:
		c.mu.Lock()
		if c.disconnectAck != nil {
			close(c.disconnectAck)
			c.disconnectAck = nil
		}
		c.mu.Unlock()
	case *pb.ListEntitiesDoneResponse:
		c.mu.Lock()
		if c.listing != nil {
			close(c.listing.done)
			c.listing = nil
		}
		c.mu.Unlock()
	case *pb.SelectStateResponse:
		c.emit(State{Key: m.GetKey(), Value: m.GetState(), Missing: m.GetMissingState()})
	case *pb.TextSensorStateResponse:
		c.emit(State{Key: m.GetKey(), Value: m.GetState(), Missing: m.GetMissingState()})
	default:
		if e, ok := entityFromMessage(msg); ok {
			c.mu.Lock()
			if c.listing != nil {
				c.listing.entities = append(c.listing.entities, e)
			}
			c.mu.Unlock()
		}
	}
}

func (c *Conn) emit(s State) {
	c.mu.Lock()
	fn := c.onState
	c.mu.Unlock()
	if fn != nil {
		fn(s)
	}
}

// keepAliveLoop pings the device when it has been silent for one interval
// and closes the connection when it stays silent too long
// (aioesphomeapi/connection.py, _async_send_keep_alive).
func (c *Conn) keepAliveLoop() {
	timeout := time.Duration(float64(c.keepAlive) * keepAliveTimeoutRatio)
	ticker := time.NewTicker(c.keepAlive / 2)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
		}
		idle := c.idle()
		if idle > timeout {
			c.fail(fmt.Errorf("%w: no data from the device for %v", ErrTimeout, idle.Round(time.Second)))
			return
		}
		if idle >= c.keepAlive {
			if err := c.send(context.Background(), &pb.PingRequest{}); err != nil {
				return
			}
		}
	}
}

func (c *Conn) touch() { c.lastRecv.Store(int64(time.Since(c.start))) }

func (c *Conn) idle() time.Duration {
	return time.Since(c.start) - time.Duration(c.lastRecv.Load())
}

// fail closes the connection once, recording why.
func (c *Conn) fail(err error) {
	c.closeOnce.Do(func() {
		c.err = err
		close(c.done)
		c.nc.Close()
	})
}
