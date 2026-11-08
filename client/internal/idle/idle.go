// Package idle tells whether anybody is using this PC: the session is not
// locked and there was keyboard or mouse input recently.
package idle

import "time"

// Threshold is how long without input counts as away.
const Threshold = 10 * time.Minute
