package espapi

import "errors"

// Errors returned by this package, usually wrapped with more detail. Test
// them with errors.Is. They mirror the exceptions of aioesphomeapi
// (aioesphomeapi/core.py) so failures can be compared with Home Assistant.
var (
	// ErrClosed means the connection is closed, by us or by the peer.
	ErrClosed = errors.New("espapi: connection closed")
	// ErrTimeout means the device stopped answering (no traffic within the
	// keepalive timeout) or did not complete a request in time.
	ErrTimeout = errors.New("espapi: timeout")
	// ErrProtocol means the device sent data that violates the protocol.
	ErrProtocol = errors.New("espapi: protocol error")
	// ErrRequiresEncryption means the device uses Noise encryption but no
	// encryption key is configured (RequiresEncryptionAPIError).
	ErrRequiresEncryption = errors.New("espapi: device requires an encryption key")
	// ErrBadName means the device reported a different name than expected
	// (BadNameAPIError).
	ErrBadName = errors.New("espapi: unexpected device name")
	// ErrIncompatibleAPI means the device speaks an API major version this
	// client does not support.
	ErrIncompatibleAPI = errors.New("espapi: incompatible API version")
	// ErrDisconnected means the device asked to close the connection.
	ErrDisconnected = errors.New("espapi: disconnected by the device")
)
