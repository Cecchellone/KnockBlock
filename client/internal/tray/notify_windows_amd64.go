package tray

import "unsafe"

// sizeof(NOTIFYICONDATAW) on 64-bit Windows is 976 bytes; the build fails
// if notifyIconData drifts from it.
var (
	_ [976 - unsafe.Sizeof(notifyIconData{})]byte
	_ [unsafe.Sizeof(notifyIconData{}) - 976]byte
)
