package status

import (
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/detect"
)

// Status IDs understood by the device (firmware/common/core.yaml). They are
// identifiers, not text: the tray and the display localize them.
const (
	Free    = "free"
	Muted   = "muted" // in a call with the microphone muted and no camera
	Mic     = "mic"
	Camera  = "camera"
	Call    = "call"
	DND     = "dnd"
	Offline = "offline"
)

// Override is the manual choice from the tray menu.
type Override int

// Overrides; Auto follows detection.
const (
	Auto Override = iota
	ForceFree
	ForceDND
)

// Result is what this PC reports to the device.
type Result struct {
	Status string
	App    string // friendly name of the app causing Status; may be empty
}

// Compute derives this PC's status from what is in use (CLAUDE.md §5.3):
// microphone and camera → call, camera → camera, microphone → mic, a
// microphone kept open but muted (a call with everything off) → muted,
// nothing → free. A manual override wins. The app is the one using the
// camera if any, else the microphone; among several, a known app first,
// then by name, so the choice is stable between polls. With sendApps false
// the app is always empty.
func Compute(s *detect.Snapshot, o Override, sendApps bool) Result {
	switch o {
	case ForceFree:
		return Result{Status: Free}
	case ForceDND:
		return Result{Status: DND}
	}
	mic, cam := false, len(s.Camera) > 0
	for _, a := range s.Mic {
		mic = mic || !a.Muted
	}
	var r Result
	switch {
	case mic && cam:
		r.Status = Call
	case cam:
		r.Status = Camera
	case mic:
		r.Status = Mic
	case len(s.Mic) > 0:
		r.Status = Muted
	default:
		return Result{Status: Free}
	}
	if sendApps {
		if cam {
			r.App = pick(s.Camera, false)
		} else {
			r.App = pick(s.Mic, mic)
		}
	}
	return r
}

// pick chooses the app to show among those using a device, only among the
// unmuted ones when live is true. apps is sorted by name
// (detect.Snapshot), so the first known app, else the first app, is
// stable.
func pick(apps []detect.App, live bool) string {
	first := ""
	for _, a := range apps {
		if live && a.Muted {
			continue
		}
		if a.Known {
			return a.Name
		}
		if first == "" {
			first = a.Name
		}
	}
	return first
}

// HeartbeatInterval is how often an unchanged status is sent again: well
// within the device's 120 s source timeout (docs/presence.md).
const HeartbeatInterval = 30 * time.Second

// Reporter decides when to send a Result: on every change, after every
// (re)connection, and as a heartbeat. It does no I/O; the caller sends and
// then calls Sent. Not safe for concurrent use.
type Reporter struct {
	sent    Result
	sentAt  time.Time
	sentGen uint64
	valid   bool
}

// Due reports whether want must be sent now on connection generation gen
// (a number that changes on every new connection).
func (r *Reporter) Due(want Result, now time.Time, gen uint64) bool {
	return !r.valid || gen != r.sentGen || want != r.sent || now.Sub(r.sentAt) >= HeartbeatInterval
}

// Sent records a successful send.
func (r *Reporter) Sent(res Result, now time.Time, gen uint64) {
	r.sent, r.sentAt, r.sentGen, r.valid = res, now, gen, true
}
