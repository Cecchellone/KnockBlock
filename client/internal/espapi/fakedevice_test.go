package espapi

import (
	"bufio"
	"net"
	"sync"
	"testing"

	"github.com/flynn/noise"
	"google.golang.org/protobuf/proto"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi/pb"
)

// Entity keys of the fake device.
const (
	keyStatus      = 101
	keyLanguage    = 102
	keyHeartbeat   = 103
	keyStatusLabel = 104
	keyReport      = 105
)

// fakeDevice is a minimal ESPHome device for tests: it speaks the server
// side of the protocol over the same transports as the client, with the
// entities of firmware/common/core.yaml.
type fakeDevice struct {
	t  *testing.T
	ln net.Listener

	// Behaviour switches, set before the client connects.
	name        string
	apiMajor    uint32
	ignorePings bool   // never answer PingRequest
	preamble    []byte // if set, sent before closing, instead of speaking the protocol
	psk         []byte // if set, the device requires Noise with this key

	mu        sync.Mutex
	status    string
	language  string
	presses   int
	pings     int
	pongs     int
	reports   [][]string // report_presence calls: pc, status, app
	hello     *pb.HelloRequest
	gotBye    bool
	conn      net.Conn
	tr        transport
	writeMu   sync.Mutex
	connected chan struct{}
}

func newFakeDevice(t *testing.T) *fakeDevice {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDevice{
		t:         t,
		ln:        ln,
		name:      "knockblock",
		apiMajor:  1,
		status:    "offline",
		language:  "en",
		connected: make(chan struct{}),
	}
	t.Cleanup(func() {
		ln.Close()
		d.mu.Lock()
		if d.conn != nil {
			d.conn.Close()
		}
		d.mu.Unlock()
	})
	return d
}

func (d *fakeDevice) addr() string { return d.ln.Addr().String() }

// serve accepts one client and handles it until it disconnects.
func (d *fakeDevice) serve() {
	go func() {
		nc, err := d.ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(nc)
		d.mu.Lock()
		d.conn = nc
		d.mu.Unlock()
		close(d.connected)
		if d.preamble != nil {
			nc.Write(d.preamble)
			nc.Close()
			return
		}
		var tr transport = newPlaintextTransport(br, nc)
		if d.psk != nil {
			var ok bool
			if tr, ok = d.noiseHandshake(br, nc); !ok {
				nc.Close()
				return
			}
		}
		d.mu.Lock()
		d.tr = tr
		d.mu.Unlock()
		d.loop()
	}()
}

func (d *fakeDevice) loop() {
	table := messages()
	for {
		id, payload, err := d.tr.readFrame()
		if err != nil {
			return
		}
		if id == idOf((*pb.DeviceInfoRequest)(nil)) {
			// Not used by this client, but aioesphomeapi waits for it
			// (interop test).
			d.send(&pb.DeviceInfoResponse{Name: d.name, MacAddress: "AA:BB:CC:DD:EE:FF", EsphomeVersion: "2026.10.0"})
			continue
		}
		mt, ok := table.byID[id]
		if !ok {
			continue
		}
		msg := mt.New().Interface()
		if err := proto.Unmarshal(payload, msg); err != nil {
			d.t.Errorf("fake device: decoding %d: %v", id, err)
			return
		}
		if !d.handle(msg) {
			return
		}
	}
}

func (d *fakeDevice) handle(msg proto.Message) bool {
	switch m := msg.(type) {
	case *pb.HelloRequest:
		d.mu.Lock()
		d.hello = m
		d.mu.Unlock()
		d.send(&pb.HelloResponse{ApiVersionMajor: d.apiMajor, ApiVersionMinor: 19, ServerInfo: "fake 2026.10.0", Name: d.name})
	case *pb.ListEntitiesRequest:
		d.send(&pb.ListEntitiesSelectResponse{ObjectId: "status", Key: keyStatus, Name: "Status",
			Options: []string{"free", "mic", "camera", "call", "dnd", "offline"}})
		d.send(&pb.ListEntitiesSelectResponse{ObjectId: "display_language", Key: keyLanguage, Name: "Display language",
			Options: []string{"en", "it"}})
		// No object_id: the client must derive it from the name.
		d.send(&pb.ListEntitiesButtonResponse{Key: keyHeartbeat, Name: "Heartbeat"})
		d.send(&pb.ListEntitiesTextSensorResponse{ObjectId: "status_label", Key: keyStatusLabel, Name: "Status label"})
		d.send(&pb.ListEntitiesServicesResponse{Name: "report_presence", Key: keyReport, Args: []*pb.ListEntitiesServicesArgument{
			{Name: "pc", Type: pb.ServiceArgType_SERVICE_ARG_TYPE_STRING},
			{Name: "status", Type: pb.ServiceArgType_SERVICE_ARG_TYPE_STRING},
			{Name: "app", Type: pb.ServiceArgType_SERVICE_ARG_TYPE_STRING},
		}})
		d.send(&pb.ListEntitiesSwitchResponse{ObjectId: "other", Key: 999, Name: "Other"}) // not handled
		d.send(&pb.ListEntitiesDoneResponse{})
	case *pb.SubscribeStatesRequest:
		d.mu.Lock()
		status, language := d.status, d.language
		d.mu.Unlock()
		d.send(&pb.SelectStateResponse{Key: keyStatus, State: status})
		d.send(&pb.SelectStateResponse{Key: keyLanguage, State: language})
	case *pb.SelectCommandRequest:
		d.mu.Lock()
		switch m.GetKey() {
		case keyStatus:
			d.status = m.GetState()
		case keyLanguage:
			d.language = m.GetState()
		}
		d.mu.Unlock()
		d.send(&pb.SelectStateResponse{Key: m.GetKey(), State: m.GetState()})
	case *pb.ExecuteServiceRequest:
		if m.GetKey() == keyReport {
			var call []string
			for _, a := range m.GetArgs() {
				call = append(call, a.GetString_())
			}
			d.mu.Lock()
			d.reports = append(d.reports, call)
			d.mu.Unlock()
		}
	case *pb.ButtonCommandRequest:
		d.mu.Lock()
		if m.GetKey() == keyHeartbeat {
			d.presses++
		}
		d.mu.Unlock()
	case *pb.PingRequest:
		d.mu.Lock()
		d.pings++
		d.mu.Unlock()
		if !d.ignorePings {
			d.send(&pb.PingResponse{})
		}
	case *pb.PingResponse:
		d.mu.Lock()
		d.pongs++
		d.mu.Unlock()
	case *pb.DisconnectRequest:
		d.mu.Lock()
		d.gotBye = true
		d.mu.Unlock()
		d.send(&pb.DisconnectResponse{})
		return false
	}
	return true
}

// noiseHandshake runs the device side of the Noise handshake, as
// api_frame_helper_noise.cpp does, and reports whether it succeeded.
func (d *fakeDevice) noiseHandshake(br *bufio.Reader, nc net.Conn) (transport, bool) {
	var buf []byte
	if hello, err := readNoiseFrame(br, &buf); err != nil || len(hello) != 0 {
		d.t.Errorf("fake device: ClientHello %x, %v", hello, err)
		return nil, false
	}
	msg, err := readNoiseFrame(br, &buf)
	if err != nil || len(msg) == 0 || msg[0] != 0 {
		d.t.Errorf("fake device: handshake frame %x, %v", msg, err)
		return nil, false
	}
	nc.Write(appendNoiseFrame(nil, []byte("\x01"+d.name+"\x00AA:BB:CC:DD:EE:FF\x00")))

	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:  noiseSuite,
		Pattern:      noise.HandshakeNN,
		Prologue:     noisePrologue,
		PresharedKey: d.psk,
	})
	if err != nil {
		d.t.Errorf("fake device: %v", err)
		return nil, false
	}
	if _, _, _, err := hs.ReadMessage(nil, msg[1:]); err != nil {
		nc.Write(appendNoiseFrame(nil, []byte("\x01"+invalidKeyReason)))
		return nil, false
	}
	reply, toDevice, toClient, err := hs.WriteMessage(nil, nil)
	if err != nil {
		d.t.Errorf("fake device: %v", err)
		return nil, false
	}
	nc.Write(appendNoiseFrame(nil, append([]byte{0}, reply...)))
	return newNoiseTransport(br, nc, toClient, toDevice), true
}

// send writes a message to the client.
func (d *fakeDevice) send(m proto.Message) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	b, err := proto.Marshal(m)
	if err != nil {
		d.t.Errorf("fake device: marshal: %v", err)
		return
	}
	id := messageID(m)
	if id == 0 {
		id = idOf(m)
	}
	d.tr.writeFrame(id, b)
}

// sendRaw writes a frame with an arbitrary type.
func (d *fakeDevice) sendRaw(id uint32, payload []byte) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.tr.writeFrame(id, payload)
}

// idOf reads the (id) option of messages the client does not handle.
func idOf(m proto.Message) uint32 {
	return proto.GetExtension(m.ProtoReflect().Descriptor().Options(), pb.E_Id).(uint32)
}

func (d *fakeDevice) snapshot() (status, language string, presses, pings int, gotBye bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status, d.language, d.presses, d.pings, d.gotBye
}
