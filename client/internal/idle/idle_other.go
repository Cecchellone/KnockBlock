//go:build !windows

package idle

import "time"

// Away is false where it cannot be told.
func Away(time.Duration) bool { return false }
