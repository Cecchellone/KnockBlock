package settingsui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
)

// DeviceConn is the part of *espapi.Conn the Light tab uses.
type DeviceConn interface {
	ListEntities(ctx context.Context) (espapi.Entities, error)
	SubscribeStates(ctx context.Context, fn func(espapi.State)) error
	SetSelect(ctx context.Context, key uint32, option string) error
	SetSwitch(ctx context.Context, key uint32, on bool) error
	SetLight(ctx context.Context, key uint32, on bool, brightness float64) error
	SetNumber(ctx context.Context, key uint32, value float64) error
	DeviceName() string
	Close() error
}

func espapiDial(ctx context.Context, opts espapi.Options) (DeviceConn, error) {
	c, err := espapi.Dial(ctx, opts)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// DeviceSettings are the light's own settings: the same entities Home
// Assistant shows. The light stores them, so they apply to every PC. A
// setting the firmware lacks has its Has… field false.
type DeviceSettings struct {
	Language     string // display language code
	Languages    []string
	Flip         bool
	HasFlip      bool
	Privacy      bool
	HasPrivacy   bool
	Backlight    int // display brightness, percent; 0 = off
	HasBacklight bool
	LED          int // default LED brightness, percent
	HasLED       bool
}

// Entities of the light, by object ID (firmware/common/*.yaml).
const (
	entLanguage  = "display_language"
	entFlip      = "flip_display"
	entPrivacy   = "privacy_mode"
	entBacklight = "display_backlight"
	entLED       = "led_brightness"
)

// Device is an open connection to the light for its settings, while the
// settings window is open.
type Device struct {
	conn  DeviceConn
	ents  espapi.Entities
	Name  string
	saved DeviceSettings // as last read or written
}

// stateWait bounds how long OpenDevice waits for the light to send the
// current values after subscribing.
const stateWait = 3 * time.Second

// OpenDevice connects to the light with the saved address and key and reads
// its settings.
func (m *Model) OpenDevice(ctx context.Context) (*Device, error) {
	cfg := m.saved()
	key, err := cfg.Key()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := m.deps.Dial(ctx, espapi.Options{Address: cfg.Address(), EncryptionKey: key, ClientInfo: m.deps.ClientInfo})
	if err != nil {
		return nil, err
	}
	d, err := readDevice(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

func readDevice(ctx context.Context, conn DeviceConn) (*Device, error) {
	ents, err := conn.ListEntities(ctx)
	if err != nil {
		return nil, err
	}
	d := &Device{conn: conn, ents: ents, Name: conn.DeviceName()}
	want := map[uint32]bool{}
	for _, id := range []struct {
		kind espapi.EntityKind
		id   string
	}{{espapi.KindSelect, entLanguage}, {espapi.KindSwitch, entFlip}, {espapi.KindSwitch, entPrivacy},
		{espapi.KindLight, entBacklight}, {espapi.KindNumber, entLED}} {
		if e, ok := ents.Find(id.kind, id.id); ok {
			want[e.Key] = true
		}
	}
	var mu sync.Mutex
	states := map[uint32]espapi.State{}
	all := make(chan struct{})
	if err := conn.SubscribeStates(ctx, func(st espapi.State) {
		mu.Lock()
		defer mu.Unlock()
		if !want[st.Key] || st.Missing {
			return
		}
		_, had := states[st.Key]
		states[st.Key] = st
		if !had && len(states) == len(want) {
			close(all)
		}
	}); err != nil {
		return nil, err
	}
	if len(want) > 0 {
		t := time.NewTimer(stateWait)
		select {
		case <-all:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}
	mu.Lock()
	defer mu.Unlock()
	s := &d.saved
	if e, ok := ents.Find(espapi.KindSelect, entLanguage); ok {
		if st, ok := states[e.Key]; ok {
			s.Language, s.Languages = st.Value, e.Options
		}
	}
	if e, ok := ents.Find(espapi.KindSwitch, entFlip); ok {
		if st, ok := states[e.Key]; ok {
			s.Flip, s.HasFlip = st.Value == "on", true
		}
	}
	if e, ok := ents.Find(espapi.KindSwitch, entPrivacy); ok {
		if st, ok := states[e.Key]; ok {
			s.Privacy, s.HasPrivacy = st.Value == "on", true
		}
	}
	if e, ok := ents.Find(espapi.KindLight, entBacklight); ok {
		if st, ok := states[e.Key]; ok {
			s.HasBacklight = true
			if st.Value == "on" {
				s.Backlight = int(math.Round(st.Number * 100))
			}
		}
	}
	if e, ok := ents.Find(espapi.KindNumber, entLED); ok {
		if st, ok := states[e.Key]; ok {
			s.LED, s.HasLED = int(math.Round(st.Number)), true
		}
	}
	return d, nil
}

// Settings returns the light's settings as last read or written.
func (d *Device) Settings() DeviceSettings { return d.saved }

// Apply sends the settings that differ from the light's current ones.
func (d *Device) Apply(ctx context.Context, s DeviceSettings) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cur := d.saved
	var errs []error
	send := func(kind espapi.EntityKind, id string, fn func(key uint32) error) {
		if e, ok := d.ents.Find(kind, id); ok {
			errs = append(errs, fn(e.Key))
		}
	}
	if s.Language != "" && s.Language != cur.Language {
		send(espapi.KindSelect, entLanguage, func(k uint32) error { return d.conn.SetSelect(ctx, k, s.Language) })
	}
	if s.HasFlip && s.Flip != cur.Flip {
		send(espapi.KindSwitch, entFlip, func(k uint32) error { return d.conn.SetSwitch(ctx, k, s.Flip) })
	}
	if s.HasPrivacy && s.Privacy != cur.Privacy {
		send(espapi.KindSwitch, entPrivacy, func(k uint32) error { return d.conn.SetSwitch(ctx, k, s.Privacy) })
	}
	if s.HasBacklight && s.Backlight != cur.Backlight {
		send(espapi.KindLight, entBacklight, func(k uint32) error {
			return d.conn.SetLight(ctx, k, s.Backlight > 0, float64(s.Backlight)/100)
		})
	}
	if s.HasLED && s.LED != cur.LED {
		send(espapi.KindNumber, entLED, func(k uint32) error { return d.conn.SetNumber(ctx, k, float64(s.LED)) })
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("settingsui: light settings: %w", err)
	}
	d.saved = s
	return nil
}

// Close disconnects from the light.
func (d *Device) Close() error { return d.conn.Close() }

// DisplayLanguages names the light's display languages, in their own
// language when the app knows it ("Italiano"), else by code.
func (m *Model) DisplayLanguages(codes []string) []Choice {
	out := make([]Choice, 0, len(codes))
	for _, code := range codes {
		name := m.deps.Catalog.Messages(code)["lang.name"]
		if name == "" {
			name = code
		}
		out = append(out, Choice{code, name})
	}
	return out
}

// Percentages lists brightness choices from 10 % to 100 %; withOff adds
// "Off" (0) first.
func (m *Model) Percentages(withOff bool) []Choice {
	var out []Choice
	if withOff {
		out = append(out, Choice{"0", m.T("settings.off")})
	}
	for p := 10; p <= 100; p += 10 {
		out = append(out, Choice{strconv.Itoa(p), strconv.Itoa(p) + " %"})
	}
	return out
}

// NearestPercent rounds p to the nearest choice value, so a brightness set
// elsewhere (e.g. 35 % from Home Assistant) still selects an entry.
func NearestPercent(p int, withOff bool) int {
	if withOff && p <= 0 {
		return 0
	}
	n := int(math.Round(float64(p)/10)) * 10
	return min(100, max(10, n))
}

// DeviceError returns the message shown when the light's settings cannot
// be read or written.
func (m *Model) DeviceError(err error) string {
	return m.T("settings.device_error", "error", err.Error())
}
