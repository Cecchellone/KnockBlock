package tray

import (
	"errors"
	"image/color"

	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
	"github.com/Cecchellone/KnockBlock/client/internal/status"
)

// Snapshot is everything the tray shows.
type Snapshot struct {
	Result status.Result // this PC's status
	Link   link.State
	Device string // device host, for messages
}

// View is the rendered tray: localized texts and the icon colour.
type View struct {
	Status     string // "In a call (Teams)"
	Connection string // "Connected to knockblock.local"
	Tooltip    string
	Color      color.NRGBA
}

// Status colours, matching the device LED (firmware/common/board.yaml);
// grey while the device is not reachable.
var statusColors = map[string]color.NRGBA{
	status.Free:   {0x00, 0xb0, 0x50, 0xff},
	status.Mic:    {0xff, 0xb0, 0x00, 0xff},
	status.Camera: {0xff, 0x60, 0x00, 0xff},
	status.Call:   {0xe0, 0x00, 0x00, 0xff},
	status.DND:    {0x80, 0x00, 0xc0, 0xff},
}

// Disconnected is the icon colour while the device is unreachable.
var Disconnected = color.NRGBA{0x80, 0x80, 0x80, 0xff}

// ColorFor returns the icon colour for a status ID.
func ColorFor(statusID string, connected bool) color.NRGBA {
	if c, ok := statusColors[statusID]; ok && connected {
		return c
	}
	return Disconnected
}

// maxTooltip is the Windows notification-area tooltip limit (128 UTF-16
// units including the terminator), kept in runes for simplicity.
const maxTooltip = 127

// Render localizes a snapshot.
func Render(t *i18n.Translator, s Snapshot) View {
	st := t.T("status." + s.Result.Status)
	if s.Result.App != "" {
		st = t.T("status.with_app", "status", st, "app", s.Result.App)
	}
	var conn string
	switch {
	case s.Device == "":
		conn = t.T("conn.not_configured")
	case s.Link.Connected:
		conn = t.T("conn.connected", "device", s.Device)
	case errors.Is(s.Link.Err, link.ErrNoPresenceAction):
		conn = t.T("conn.old_firmware", "device", s.Device)
	case s.Link.NeedsConfig:
		conn = t.T("conn.bad_key", "device", s.Device)
	case s.Link.Err != nil:
		conn = t.T("conn.unreachable", "device", s.Device)
	default:
		conn = t.T("conn.connecting", "device", s.Device)
	}
	tip := []rune(t.T("tooltip", "status", st, "connection", conn))
	if len(tip) > maxTooltip {
		tip = append(tip[:maxTooltip-1], '…')
	}
	return View{
		Status:     st,
		Connection: conn,
		Tooltip:    string(tip),
		Color:      ColorFor(s.Result.Status, s.Link.Connected),
	}
}
