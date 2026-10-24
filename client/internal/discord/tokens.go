package discord

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// FileTokens keeps the tokens in a file, encrypted by protect (DPAPI in the
// app).
type FileTokens struct {
	Path      string
	Protect   func([]byte) ([]byte, error)
	Unprotect func([]byte) ([]byte, error)
}

// Load reads the tokens; ok is false when there are none.
func (f FileTokens) Load() (Token, bool) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return Token{}, false
	}
	if b, err = f.Unprotect(b); err != nil {
		return Token{}, false
	}
	var t Token
	if json.Unmarshal(b, &t) != nil || t.Access == "" {
		return Token{}, false
	}
	return t, true
}

// Save writes the tokens.
func (f FileTokens) Save(t Token) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if b, err = f.Protect(b); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.Path, b, 0o600)
}

// Clear forgets the tokens.
func (f FileTokens) Clear() error {
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
