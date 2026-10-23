package tray

import (
	"errors"
	"strconv"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/link"
)

// UnreachableAfter is how long the device must stay unreachable before a
// notification (CLAUDE.md §5.4: only for things that matter).
const UnreachableAfter = 5 * time.Minute

// Alert is a notification to show: a message key and its arguments.
type Alert struct {
	Key  string
	Args []string
}

// Alerts decides when to notify: once when the device has been
// unreachable for UnreachableAfter, once for a configuration problem
// (wrong key, outdated firmware). A successful connection resets both.
type Alerts struct {
	downSince       time.Time
	unreachableSent bool
	configSent      bool
}

// Update takes the current link state; call it on every state change and
// periodically, so time passing is noticed.
func (a *Alerts) Update(st link.State, now time.Time) (Alert, bool) {
	if st.Connected {
		*a = Alerts{}
		return Alert{}, false
	}
	if st.Err == nil {
		return Alert{}, false // still connecting for the first time
	}
	if st.NeedsConfig {
		if a.configSent {
			return Alert{}, false
		}
		a.configSent = true
		if errors.Is(st.Err, link.ErrNoPresenceAction) {
			return Alert{Key: "notify.old_firmware"}, true
		}
		return Alert{Key: "notify.bad_key"}, true
	}
	if a.downSince.IsZero() {
		a.downSince = now
	}
	if !a.unreachableSent && now.Sub(a.downSince) >= UnreachableAfter {
		a.unreachableSent = true
		minutes := int(now.Sub(a.downSince) / time.Minute)
		return Alert{Key: "notify.unreachable", Args: []string{"minutes", strconv.Itoa(minutes)}}, true
	}
	return Alert{}, false
}
