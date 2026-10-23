//go:build windows

package config

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// protect encrypts data for the current Windows user with DPAPI
// (CryptProtectData, CLAUDE.md §5.5).
func protect(data []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(data)), Data: unsafe.SliceData(data)}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

func unprotect(data []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(data)), Data: unsafe.SliceData(data)}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

// takeBlob copies a DPAPI output blob and frees it.
func takeBlob(b windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
	return append([]byte(nil), unsafe.Slice(b.Data, b.Size)...)
}
