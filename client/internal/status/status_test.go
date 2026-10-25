package status

import (
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/detect"
)

var (
	teams   = detect.App{Name: "Teams", Known: true}
	discord = detect.App{Name: "Discord", Known: true}
	obs     = detect.App{Name: "OBS", Known: true}
	unknown = detect.App{Name: "Arecorder"} // sorts before known names
	mutedDC = detect.App{Name: "Discord", Known: true, Muted: true}
	mutedTm = detect.App{Name: "Teams", Known: true, Muted: true}
)

func TestCompute(t *testing.T) {
	tests := []struct {
		name     string
		mic, cam []detect.App
		override Override
		sendApps bool
		want     Result
	}{
		{"nothing", nil, nil, Auto, true, Result{Status: Free, App: ""}},
		{"mic only", []detect.App{discord}, nil, Auto, true, Result{Status: Mic, App: "Discord"}},
		{"camera only", nil, []detect.App{obs}, Auto, true, Result{Status: Camera, App: "OBS"}},
		{"both: camera app wins", []detect.App{discord}, []detect.App{teams}, Auto, true, Result{Status: Call, App: "Teams"}},
		{"known app before unknown", []detect.App{unknown, discord}, nil, Auto, true, Result{Status: Mic, App: "Discord"}},
		{"only unknown apps", []detect.App{unknown}, nil, Auto, true, Result{Status: Mic, App: "Arecorder"}},
		{"app names off", []detect.App{discord}, []detect.App{teams}, Auto, false, Result{Status: Call, App: ""}},
		{"muted call", []detect.App{mutedDC}, nil, Auto, true, Result{Status: Muted, App: "Discord"}},
		{"muted mic, camera on", []detect.App{mutedDC}, []detect.App{discord}, Auto, true, Result{Status: Camera, App: "Discord"}},
		{"one app muted, one live", []detect.App{mutedDC, teams}, nil, Auto, true, Result{Status: Mic, App: "Teams"}},
		{"live unknown before muted known", []detect.App{unknown, mutedTm}, nil, Auto, true, Result{Status: Mic, App: "Arecorder"}},
		{"override dnd", []detect.App{discord}, nil, ForceDND, true, Result{Status: DND, App: ""}},
		{"override free in a call", []detect.App{discord}, []detect.App{teams}, ForceFree, true, Result{Status: Free, App: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &detect.Snapshot{Mic: tt.mic, Camera: tt.cam}
			got := Compute(s, tt.override, tt.sendApps)
			if got.Status != tt.want.Status || got.App != tt.want.App {
				t.Errorf("Compute = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDevices(t *testing.T) {
	tests := []struct {
		name     string
		mic, cam []detect.App
		override Override
		want     string
	}{
		{"nothing", nil, nil, Auto, ""},
		{"call", []detect.App{discord}, []detect.App{discord}, Auto, "call,mic,camera"},
		{"muted call", []detect.App{mutedDC}, nil, Auto, "call,mic_muted"},
		{"one app live", []detect.App{mutedDC, teams}, nil, Auto, "call,mic"},
		{"camera only", nil, []detect.App{obs}, Auto, "camera"},
		{"kept with an override", []detect.App{mutedDC}, []detect.App{discord}, ForceDND, "call,mic_muted,camera"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &detect.Snapshot{Mic: tt.mic, Camera: tt.cam}
			if got := Compute(s, tt.override, true).Devices(); got != tt.want {
				t.Errorf("Devices = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReporter(t *testing.T) {
	var r Reporter
	t0 := time.Unix(1000, 0)
	call := Result{Status: Call, App: "Teams"}
	free := Result{Status: Free, App: ""}

	steps := []struct {
		name string
		want Result
		at   time.Duration
		gen  uint64
		due  bool
	}{
		{"first report", call, 0, 1, true},
		{"unchanged", call, 5 * time.Second, 1, false},
		{"changed", free, 6 * time.Second, 1, true},
		{"unchanged again", free, 20 * time.Second, 1, false},
		{"heartbeat", free, 36 * time.Second, 1, true},
		{"reconnected", free, 37 * time.Second, 2, true},
		{"after reconnect", free, 38 * time.Second, 2, false},
	}
	for _, s := range steps {
		now := t0.Add(s.at)
		if got := r.Due(s.want, now, s.gen); got != s.due {
			t.Fatalf("%s: Due = %v, want %v", s.name, got, s.due)
		}
		if s.due {
			r.Sent(s.want, now, s.gen)
		}
	}
}
