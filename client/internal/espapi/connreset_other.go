//go:build !windows

package espapi

import (
	"errors"
	"syscall"
)

func isConnReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED)
}
