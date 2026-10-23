//go:build !windows

package detect

// NewConsentStore returns ErrUnsupported: the consent store exists only on
// Windows. Use FromFile or a fake in tests.
func NewConsentStore(Options) (Detector, error) { return nil, ErrUnsupported }

// DumpConsentStore returns ErrUnsupported: the consent store exists only on
// Windows.
func DumpConsentStore(func(dev Device, key string, packaged bool, start, stop uint64)) error {
	return ErrUnsupported
}

// DumpAudioSessions returns ErrUnsupported: the audio sessions are read
// only on Windows.
func DumpAudioSessions(func(pid uint32, exe, path string, active bool)) error {
	return ErrUnsupported
}
