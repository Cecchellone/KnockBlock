package settingsui

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
)

// fakeLight is a light with the board's settings entities.
type fakeLight struct {
	mu       sync.Mutex
	commands []string
	noLED    bool
	closed   bool
}

func (f *fakeLight) ListEntities(context.Context) (espapi.Entities, error) {
	ents := espapi.Entities{
		{Kind: espapi.KindSelect, ObjectID: "display_language", Key: 1, Options: []string{"en", "it", "xx"}},
		{Kind: espapi.KindSwitch, ObjectID: "flip_display", Key: 2},
		{Kind: espapi.KindSwitch, ObjectID: "privacy_mode", Key: 3},
		{Kind: espapi.KindLight, ObjectID: "display_backlight", Key: 4},
	}
	if !f.noLED {
		ents = append(ents, espapi.Entity{Kind: espapi.KindNumber, ObjectID: "led_brightness", Key: 5})
	}
	return ents, nil
}

func (f *fakeLight) SubscribeStates(_ context.Context, fn func(espapi.State)) error {
	go func() {
		fn(espapi.State{Key: 1, Value: "it"})
		fn(espapi.State{Key: 2, Value: "on"})
		fn(espapi.State{Key: 3, Value: "off"})
		fn(espapi.State{Key: 4, Value: "on", Number: 0.35})
		if !f.noLED {
			fn(espapi.State{Key: 5, Number: 60})
		}
	}()
	return nil
}

func (f *fakeLight) record(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, s)
	return nil
}

func (f *fakeLight) SetSelect(_ context.Context, k uint32, o string) error {
	return f.record(fmt.Sprintf("%d=%s", k, o))
}
func (f *fakeLight) SetSwitch(_ context.Context, k uint32, on bool) error {
	return f.record(fmt.Sprintf("%d=%v", k, on))
}
func (f *fakeLight) SetLight(_ context.Context, k uint32, on bool, b float64) error {
	return f.record(fmt.Sprintf("%d=%v/%.2f", k, on, b))
}
func (f *fakeLight) SetNumber(_ context.Context, k uint32, v float64) error {
	return f.record(fmt.Sprintf("%d=%g", k, v))
}
func (f *fakeLight) DeviceName() string { return "knockblock" }
func (f *fakeLight) Close() error       { f.closed = true; return nil }

func TestDeviceSettings(t *testing.T) {
	light := &fakeLight{}
	var dialed espapi.Options
	m, path, _ := newModel(t, nil)
	m.deps.Dial = func(_ context.Context, o espapi.Options) (DeviceConn, error) { dialed = o; return light, nil }
	cfg := config.Default()
	cfg.Host = "luce.lan"
	cfg.SetKey(validKey)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m = Load(m.deps)

	d, err := m.OpenDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dialed.Address != "luce.lan:6053" || dialed.EncryptionKey != validKey {
		t.Errorf("dialed %+v", dialed)
	}
	s := d.Settings()
	want := DeviceSettings{Language: "it", Languages: []string{"en", "it", "xx"}, Flip: true, HasFlip: true,
		HasPrivacy: true, Backlight: 35, HasBacklight: true, LED: 60, HasLED: true}
	if !slices.Equal(s.Languages, want.Languages) || s.Language != want.Language || s.Flip != want.Flip || !s.HasFlip ||
		s.Privacy || !s.HasPrivacy || s.Backlight != 35 || !s.HasBacklight || s.LED != 60 || !s.HasLED {
		t.Fatalf("settings %+v, want %+v", s, want)
	}
	if got := m.DisplayLanguages(s.Languages); got[1] != (Choice{"it", "Italiano"}) || got[2] != (Choice{"xx", "xx"}) {
		t.Errorf("display languages %v", got)
	}

	// Only what changed is sent.
	s.Flip, s.Backlight, s.LED = false, 0, 30
	if err := d.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(light.commands, []string{"2=false", "4=false/0.00", "5=30"}) {
		t.Errorf("commands %v", light.commands)
	}
	light.commands = nil
	if err := d.Apply(context.Background(), s); err != nil || len(light.commands) != 0 {
		t.Errorf("unchanged settings sent %v (%v)", light.commands, err)
	}
	d.Close()
	if !light.closed {
		t.Error("connection left open")
	}

	// A firmware without LED brightness: the field is absent.
	light2 := &fakeLight{noLED: true}
	m.deps.Dial = func(context.Context, espapi.Options) (DeviceConn, error) { return light2, nil }
	d, err = m.OpenDevice(context.Background())
	if err != nil || d.Settings().HasLED {
		t.Errorf("no LED entity: %+v, %v", d.Settings(), err)
	}
}

func TestPercentages(t *testing.T) {
	m, _, _ := newModel(t, nil)
	p := m.Percentages(true)
	if len(p) != 11 || p[0] != (Choice{"0", "Spento"}) || p[10] != (Choice{"100", "100 %"}) {
		t.Errorf("percentages %v", p)
	}
	for in, want := range map[int]int{0: 0, 4: 10, 35: 40, 34: 30, 100: 100, 120: 100} {
		if got := NearestPercent(in, true); got != want {
			t.Errorf("NearestPercent(%d) = %d, want %d", in, got, want)
		}
	}
	if NearestPercent(0, false) != 10 {
		t.Error("without off, 0 rounds up to 10")
	}
}
