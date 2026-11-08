//go:build windows

package tray

import (
	"os"
	"testing"
	"time"
)

// TestReplyToastManual shows a real doorbell toast and waits for a reply.
// It runs only with KNOCKBLOCK_TOAST_TEST=1, on a desktop session.
func TestReplyToastManual(t *testing.T) {
	if os.Getenv("KNOCKBLOCK_TOAST_TEST") != "1" {
		t.Skip("set KNOCKBLOCK_TOAST_TEST=1 to show a real toast")
	}
	got := make(chan string, 1)
	err := showReplyToast(replyToast{Title: "Doorbell", Body: "Someone is at your door.", Placeholder: "Reply on the light", Send: "Send", Quick: []string{"Come in", "5 minutes", "Not now"}},
		func(s string) { got <- s })
	if err != nil {
		t.Fatal(err)
	}
	t.Log("toast shown; waiting for a reply")
	select {
	case s := <-got:
		t.Logf("reply: %q", s)
	case <-time.After(60 * time.Second):
		t.Log("no reply within 60 s")
	}
}
