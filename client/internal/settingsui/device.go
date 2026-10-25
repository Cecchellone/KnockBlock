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
	saved DeviceSettings // as last read or saved

	send sync.Mutex // one command sequence at a time

	mu      sync.Mutex
	live    DeviceSettings // what the light shows now, previews included
	pending [sliderCount]struct {
		value int
		set   bool
	}
	wake     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// Slider is a setting the window changes live, while it is dragged.
type Slider int

// Sliders.
const (
	SliderBacklight Slider = iota // display brightness, 0–100 %, 0 = off
	SliderLED                     // default LED brightness, 5–100 %
	sliderCount
)

// sendTimeout bounds each command sent to the light.
const sendTimeout = 5 * time.Second

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
	d := &Device{conn: conn, ents: ents, Name: conn.DeviceName(),
		wake: make(chan struct{}, 1), done: make(chan struct{})}
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
	d.live = d.saved
	go d.previews()
	return d, nil
}

// Settings returns the light's settings as last read or saved.
func (d *Device) Settings() DeviceSettings {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.saved
}

// Preview shows a slider's value on the light at once, without saving it:
// Apply keeps it, Revert undoes it. It does not wait; while a command is on
// its way, newer values replace older ones, so dragging never queues up.
func (d *Device) Preview(sl Slider, value int) {
	d.mu.Lock()
	d.pending[sl].value, d.pending[sl].set = value, true
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default: // already woken
	}
}

// previews sends the pending slider values until Close.
func (d *Device) previews() {
	for {
		select {
		case <-d.done:
			return
		case <-d.wake:
		}
		for {
			d.mu.Lock()
			sl, value, ok := d.takePending()
			d.mu.Unlock()
			if !ok {
				break
			}
			d.send.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
			err := d.setSlider(ctx, sl, value)
			cancel()
			if err == nil {
				d.mu.Lock()
				d.setLive(sl, value)
				d.mu.Unlock()
			}
			d.send.Unlock()
		}
	}
}

// takePending returns and clears one pending slider value; d.mu is held.
func (d *Device) takePending() (Slider, int, bool) {
	for sl := range d.pending {
		if p := &d.pending[sl]; p.set {
			p.set = false
			return Slider(sl), p.value, true
		}
	}
	return 0, 0, false
}

// dropPending forgets the slider values not sent yet; d.mu is held.
func (d *Device) dropPending() {
	for sl := range d.pending {
		d.pending[sl].set = false
	}
}

func (d *Device) setLive(sl Slider, value int) {
	if sl == SliderBacklight {
		d.live.Backlight = value
	} else {
		d.live.LED = value
	}
}

func (d *Device) setSlider(ctx context.Context, sl Slider, value int) error {
	if sl == SliderBacklight {
		if e, ok := d.ents.Find(espapi.KindLight, entBacklight); ok {
			return d.conn.SetLight(ctx, e.Key, value > 0, float64(value)/100)
		}
		return nil
	}
	if e, ok := d.ents.Find(espapi.KindNumber, entLED); ok {
		return d.conn.SetNumber(ctx, e.Key, float64(value))
	}
	return nil
}

// Revert puts back the saved values of the sliders previewed since the last
// Apply: the window was closed while a slider was being dragged.
func (d *Device) Revert(ctx context.Context) error {
	d.mu.Lock()
	d.dropPending()
	d.mu.Unlock()
	d.send.Lock()
	defer d.send.Unlock()
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	d.mu.Lock()
	live, saved := d.live, d.saved
	d.mu.Unlock()
	var errs []error
	if saved.HasBacklight && live.Backlight != saved.Backlight {
		errs = append(errs, d.setSlider(ctx, SliderBacklight, saved.Backlight))
	}
	if saved.HasLED && live.LED != saved.LED {
		errs = append(errs, d.setSlider(ctx, SliderLED, saved.LED))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("settingsui: restoring the light's brightness: %w", err)
	}
	d.mu.Lock()
	d.live = d.saved
	d.mu.Unlock()
	return nil
}

// Apply sends the settings that differ from what the light shows now
// (previews included) and keeps them as the saved ones.
func (d *Device) Apply(ctx context.Context, s DeviceSettings) error {
	d.mu.Lock()
	d.dropPending() // s has the final values
	d.mu.Unlock()
	d.send.Lock()
	defer d.send.Unlock()
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	d.mu.Lock()
	cur := d.live
	d.mu.Unlock()
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
	d.mu.Lock()
	d.saved, d.live = s, s
	d.mu.Unlock()
	return nil
}

// Close disconnects from the light.
func (d *Device) Close() error {
	d.stopOnce.Do(func() { close(d.done) })
	return d.conn.Close()
}

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

// SliderRange returns a slider's limits and step, in percent.
func SliderRange(sl Slider) (lo, hi, step int) {
	if sl == SliderBacklight {
		return 0, 100, 5
	}
	return 5, 100, 5 // the firmware's led_brightness minimum
}

// snap rounds a value to the slider's steps and limits.
func snap(sl Slider, v int) int {
	lo, hi, step := SliderRange(sl)
	v = (v + step/2) / step * step
	return min(hi, max(lo, v))
}

// SliderLabel is the text next to a slider: "35 %", or "Off" for a display
// brightness of 0.
func (m *Model) SliderLabel(sl Slider, value int) string {
	if sl == SliderBacklight && value <= 0 {
		return m.T("settings.off")
	}
	return strconv.Itoa(value) + " %"
}

// DeviceError returns the message shown when the light's settings cannot
// be read or written.
func (m *Model) DeviceError(err error) string {
	return m.T("settings.device_error", "error", err.Error())
}
