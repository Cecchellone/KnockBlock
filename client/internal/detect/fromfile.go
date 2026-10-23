package detect

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// FromFile returns a Detector that reads the apps in use from a text file
// on every poll, for tests and for trying the agent without Windows. Each
// line is a device and a consent store key:
//
//	mic Discord.exe
//	camera MSTeams_8wekyb3d8bbwe
//
// A key ending in ".exe" is a desktop app, anything else a Store package.
// Empty lines and lines starting with '#' are ignored; a missing file means
// nothing is in use. Names and the ignore list work as for the consent store.
func FromFile(path string, opts Options) Detector {
	return newConsentStore(&fileWalker{path: path}, nil, opts)
}

type fileWalker struct {
	path string
}

func (w *fileWalker) walk(dev Device, fn func(key []byte, packaged bool, start, stop uint64)) error {
	f, err := os.Open(w.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		device, key, ok := strings.Cut(text, " ")
		key = strings.TrimSpace(key)
		if !ok || key == "" || (device != "mic" && device != "camera") {
			return fmt.Errorf("detect: %s:%d: want \"mic|camera <key>\", got %q", w.path, line, text)
		}
		if device != dev.String() {
			continue
		}
		packaged := !strings.HasSuffix(strings.ToLower(key), ".exe")
		fn([]byte(key), packaged, 1, 0) // in use
	}
	return sc.Err()
}

func (w *fileWalker) close() error { return nil }
