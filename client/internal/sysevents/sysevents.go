// Package sysevents reports system events after which the connection to
// the device should be re-established at once: resume from sleep and
// network changes (CLAUDE.md §5.3).
package sysevents

import (
	"sync"
	"time"
)

// debounce delays fn until events stop for d: network changes come in
// bursts.
type debounce struct {
	mu    sync.Mutex
	timer *time.Timer
	d     time.Duration
	fn    func(string)
}

func (b *debounce) trigger(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(b.d, func() { b.fn(reason) })
}

func (b *debounce) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
}
