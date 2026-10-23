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

// AudioSession describes one audio session for diagnostics.
type AudioSession struct {
	PID       uint32
	Exe, Path string
	Capture   bool    // a recording session; false: playback
	Active    bool    // recording or playing now
	Muted     bool    // the session or its device is muted in Windows
	Peak      float32 // level 0–1, -1 if unknown
}

// DumpAudioSessions returns ErrUnsupported: the audio sessions are read
// only on Windows.
func DumpAudioSessions(func(s AudioSession)) error { return ErrUnsupported }

// DumpCameraStreams returns ErrUnsupported: the camera streams are read
// only on Windows.
func DumpCameraStreams(wait func(), fn func(pid uint32, exe, path string)) error {
	return ErrUnsupported
}
