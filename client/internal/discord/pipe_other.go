//go:build !windows

package discord

import (
	"context"
	"errors"
	"io"
)

// dialPipe is Windows-only; tests pass their own Dial.
func dialPipe(context.Context) (io.ReadWriteCloser, error) {
	return nil, errors.New("discord: RPC pipe only on Windows")
}
