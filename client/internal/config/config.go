package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// Config is the app configuration, stored as JSON in
// %APPDATA%\KnockBlock\config.json.
type Config struct {
	// Host is the device address (default knockblock.local) and Port its
	// Native API port (default 6053).
	Host string `json:"host"`
	Port int    `json:"port"`
	// KeyProtected is the API encryption key, encrypted for the current
	// Windows user with DPAPI, base64. Use Key and SetKey. Never logged.
	KeyProtected string `json:"key_protected,omitempty"`
	// KeyPlain lets a user paste the key into config.json by hand; the app
	// moves it into KeyProtected on start (ProtectPlainKey) and saves.
	KeyPlain string `json:"key,omitempty"`
	// Language of the tray and settings page; empty follows Windows.
	Language string `json:"language,omitempty"`
	// PCName is reported for this PC; empty uses the computer name.
	PCName string `json:"pc_name,omitempty"`
	// SendAppNames reports which app uses the mic/camera.
	SendAppNames bool `json:"send_app_names"`
	// Notify is how notifications show: NotifyOff, NotifySilent,
	// NotifySound or NotifyFull. It applies to every notification (the
	// light unreachable, the doorbell, updates).
	Notify string `json:"notify"`
	// LegacyNotifications is the old on/off switch, read from older files
	// and turned into Notify by Load; never written.
	LegacyNotifications *bool `json:"notifications,omitempty"`
	// Doorbell makes this PC notify when the light's doorbell button is
	// pressed.
	Doorbell bool `json:"doorbell"`
	// Ignore lists executables or Store packages not to report.
	Ignore []string `json:"ignore,omitempty"`
	// AppNames maps executables or packages to the names shown.
	AppNames map[string]string `json:"app_names,omitempty"`
}

// Defaults.
const (
	DefaultHost = "knockblock.local"
	DefaultPort = 6053
)

// Notification modes (Config.Notify).
const (
	NotifyOff    = "off"    // no notifications
	NotifySilent = "silent" // the notification, without sound
	NotifySound  = "sound"  // only the Windows notification sound
	NotifyFull   = "full"   // the notification with the Windows sound
)

// NotifyModes lists the notification modes in the order shown.
var NotifyModes = []string{NotifyOff, NotifySilent, NotifySound, NotifyFull}

// Default returns the configuration of a fresh install.
func Default() Config {
	return Config{Host: DefaultHost, Port: DefaultPort, SendAppNames: true, Notify: NotifySilent, Doorbell: true}
}

// Path returns %APPDATA%\KnockBlock\config.json (the user config directory
// on other systems).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	return filepath.Join(dir, "KnockBlock", "config.json"), nil
}

// Load reads the configuration; a missing file gives the defaults. Fields
// missing from the file keep their defaults.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	c.Notify = "" // to tell a file without it
	if err := json.Unmarshal(data, &c); err != nil {
		return Default(), fmt.Errorf("config: %s: %w", path, err)
	}
	if !slices.Contains(NotifyModes, c.Notify) {
		c.Notify = NotifySilent
		if c.LegacyNotifications != nil && !*c.LegacyNotifications {
			c.Notify = NotifyOff // notifications were turned off
		}
	}
	c.LegacyNotifications = nil
	if c.Host == "" {
		c.Host = DefaultHost
	}
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = DefaultPort
	}
	return c, nil
}

// Save writes the configuration atomically (temporary file, then rename),
// readable only by the user.
func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "config-*.json")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// Address returns host:port for espapi.Options.Address.
func (c Config) Address() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Key returns the API encryption key, or "" if none is set.
func (c Config) Key() (string, error) {
	if c.KeyProtected == "" {
		return "", nil
	}
	blob, err := base64.StdEncoding.DecodeString(c.KeyProtected)
	if err != nil {
		return "", fmt.Errorf("config: stored key is corrupt: %w", err)
	}
	key, err := unprotect(blob)
	if err != nil {
		return "", fmt.Errorf("config: cannot decrypt the stored key (was it saved by another Windows user?): %w", err)
	}
	return string(key), nil
}

// SetKey stores the API encryption key; "" removes it.
func (c *Config) SetKey(key string) error {
	if key == "" {
		c.KeyProtected = ""
		return nil
	}
	blob, err := protect([]byte(key))
	if err != nil {
		return fmt.Errorf("config: protecting the key: %w", err)
	}
	c.KeyProtected = base64.StdEncoding.EncodeToString(blob)
	return nil
}

// ProtectPlainKey moves a hand-written plain key into KeyProtected. It
// reports whether the configuration changed and must be saved.
func (c *Config) ProtectPlainKey() (bool, error) {
	if c.KeyPlain == "" {
		return false, nil
	}
	if err := c.SetKey(c.KeyPlain); err != nil {
		return false, err
	}
	c.KeyPlain = ""
	return true, nil
}
