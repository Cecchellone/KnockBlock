package teams

import (
	"os"
	"path/filepath"
)

// FileToken keeps the token in a file, encrypted by Protect (DPAPI in the
// app).
type FileToken struct {
	Path      string
	Protect   func([]byte) ([]byte, error)
	Unprotect func([]byte) ([]byte, error)
}

// Load reads the token; ok is false when there is none.
func (f FileToken) Load() (string, bool) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return "", false
	}
	if b, err = f.Unprotect(b); err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// Save writes the token.
func (f FileToken) Save(token string) error {
	b, err := f.Protect([]byte(token))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.Path, b, 0o600)
}
