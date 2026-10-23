package sysevents

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDebounce(t *testing.T) {
	var calls atomic.Int32
	b := &debounce{d: 30 * time.Millisecond, fn: func(string) { calls.Add(1) }}
	for i := 0; i < 5; i++ {
		b.trigger("network")
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("burst of 5 events gave %d calls, want 1", n)
	}
	b.trigger("resume")
	b.stop()
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("stopped debounce still fired (%d calls)", n)
	}
}
