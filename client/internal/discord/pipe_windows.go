//go:build windows

package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// dialPipe opens Discord's RPC pipe: \\.\pipe\discord-ipc-0 to -9, the
// first one that answers (Discord, PTB and Canary take the next free
// number). The pipe is opened for overlapped I/O, so os.File can close it
// while a read waits.
func dialPipe(ctx context.Context) (io.ReadWriteCloser, error) {
	var last error = errors.New("discord: no RPC pipe")
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		name := fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i)
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
		if err != nil {
			last = err
			continue
		}
		return os.NewFile(uintptr(h), name), nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, last
}
