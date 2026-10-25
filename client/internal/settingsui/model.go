// Package settingsui is the settings window: a native window per operating
// system (Win32 on Windows, window_windows.go) over a portable model that
// holds everything else, so another system only adds its own view. The
// model loads the configuration, localizes the texts, validates and saves
// the form, and runs the connection test.
package settingsui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/discord"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
)

// Autostart reads and changes "Start with Windows".
type Autostart interface {
	Enabled() bool
	Set(enabled bool) error
}

// TestResult is the outcome of a successful connection test.
type TestResult struct {
	Device  string // device name
	Version string // ESPHome version
}

// Deps configures the settings window.
type Deps struct {
	ConfigPath string
	Catalog    *i18n.Catalog
	// SystemLanguages returns the Windows UI languages (i18n.SystemLanguages).
	SystemLanguages func() []string
	Hostname        string // default PC name
	Autostart       Autostart
	// Test connects to a device with the given options. Default: espapi.
	Test func(ctx context.Context, opts espapi.Options) (TestResult, error)
	// Dial connects to the light for its settings (the Light tab).
	// Default: espapi.
	Dial       func(ctx context.Context, opts espapi.Options) (DeviceConn, error)
	Logger     *slog.Logger
	ClientInfo string
	// Discord is the Discord integration, for its state in the
	// Integrations tab; nil when there is none.
	Discord DiscordStatus
}

// DiscordStatus tells what the Discord integration is doing.
type DiscordStatus interface {
	HasApp() bool
	State() discord.State
}

func (d *Deps) defaults() {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Test == nil {
		d.Test = espapiTest
	}
	if d.Dial == nil {
		d.Dial = espapiDial
	}
	if d.SystemLanguages == nil {
		d.SystemLanguages = func() []string { return nil }
	}
}

// Form is what the window shows and edits. Text fields hold what the user
// typed; Save validates them.
type Form struct {
	Host, Port string
	// Key is a new encryption key; empty keeps the saved one, unless
	// RemoveKey is set.
	Key       string
	RemoveKey bool
	PCName    string
	// Language is the app's language code, "" to follow Windows.
	Language           string
	SendApps, Doorbell bool
	// Notify is the notification mode (config.Notify*).
	Notify    string
	Autostart bool
	// Discord follows Discord's mute and deafen buttons.
	Discord bool
	// Ignore and Names hold one entry per line ("exe = name" for Names).
	Ignore, Names string
}

// Field names a form field, so the view can focus the one in error.
type Field int

// Fields that can be invalid.
const (
	FieldNone Field = iota
	FieldHost
	FieldPort
	FieldKey
	FieldNames
)

// FieldError is a validation error, with its message already localized.
type FieldError struct {
	Field   Field
	Message string
}

func (e *FieldError) Error() string { return e.Message }

// Choice is an entry of a drop-down list: the value stored and the text
// shown.
type Choice struct{ Value, Label string }

// Model is the state behind one open settings window.
type Model struct {
	deps Deps
	tr   *i18n.Translator

	mu  sync.Mutex // Test runs on another goroutine than Save
	cfg config.Config

	// LoadErr is set when the configuration file could not be read; the
	// form then shows the defaults.
	LoadErr error
}

// Load reads the configuration and picks the window language like the tray
// does: the language setting, else Windows, else English.
func Load(deps Deps) *Model {
	deps.defaults()
	cfg, err := config.Load(deps.ConfigPath)
	m := &Model{deps: deps, cfg: cfg, LoadErr: err}
	prefs := deps.SystemLanguages()
	if cfg.Language != "" {
		prefs = append([]string{cfg.Language}, prefs...)
	}
	m.tr = deps.Catalog.Translator(deps.Catalog.Match(prefs...))
	return m
}

// T translates a key in the window language.
func (m *Model) T(key string, args ...string) string { return m.tr.T(key, args...) }

// KeySaved reports whether an encryption key is stored.
func (m *Model) KeySaved() bool { return m.saved().KeyProtected != "" }

func (m *Model) saved() config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// HasDiscord reports whether this build can follow Discord.
func (m *Model) HasDiscord() bool { return m.deps.Discord != nil && m.deps.Discord.HasApp() }

// DiscordState describes what the Discord integration is doing, localized.
func (m *Model) DiscordState() string {
	if m.deps.Discord == nil {
		return m.T(discord.NoApp.Key())
	}
	return m.T(m.deps.Discord.State().Key())
}

// DefaultPCName is the name reported when the PC name field is empty.
func (m *Model) DefaultPCName() string { return m.deps.Hostname }

// Form returns the saved settings, ready to show.
func (m *Model) Form() Form {
	cfg := m.saved()
	names := make([]string, 0, len(cfg.AppNames))
	for _, exe := range slices.Sorted(maps.Keys(cfg.AppNames)) {
		names = append(names, exe+" = "+cfg.AppNames[exe])
	}
	return Form{
		Host: cfg.Host, Port: strconv.Itoa(cfg.Port),
		PCName: cfg.PCName, Language: cfg.Language,
		SendApps: cfg.SendAppNames, Doorbell: cfg.Doorbell, Notify: cfg.Notify,
		Autostart: m.deps.Autostart.Enabled(),
		Discord:   cfg.Discord,
		Ignore:    strings.Join(cfg.Ignore, "\n"), Names: strings.Join(names, "\n"),
	}
}

// Languages lists the app languages, each named in its own language, after
// a first entry with value "" labelled with firstKey ("follow Windows" or
// "leave unchanged").
func (m *Model) Languages(firstKey string) []Choice {
	out := []Choice{{"", m.T(firstKey)}}
	for _, code := range m.deps.Catalog.Languages() {
		name := m.deps.Catalog.Messages(code)["lang.name"]
		if name == "" {
			name = code
		}
		out = append(out, Choice{code, name})
	}
	return out
}

// Orientations lists the display orientation choices: "off" (normal) and
// "on" (upside down).
func (m *Model) Orientations() []Choice {
	return []Choice{
		{"off", m.T("settings.display_flip_off")},
		{"on", m.T("settings.display_flip_on")},
	}
}

// NotifyModes lists the notification modes: none, silent, sound only, with
// sound.
func (m *Model) NotifyModes() []Choice {
	out := make([]Choice, 0, len(config.NotifyModes))
	for _, mode := range config.NotifyModes {
		out = append(out, Choice{mode, m.T("settings.notify_" + mode)})
	}
	return out
}

// apply builds the new configuration from the form, starting from the
// saved one so settings the window does not show are kept.
func (m *Model) apply(f Form) (config.Config, error) {
	saved := m.saved()
	cfg := saved
	fail := func(field Field, key string, args ...string) (config.Config, error) {
		return saved, &FieldError{Field: field, Message: m.T(key, args...)}
	}
	if cfg.Host = strings.TrimSpace(f.Host); cfg.Host == "" {
		return fail(FieldHost, "settings.err_host")
	}
	port, err := strconv.Atoi(strings.TrimSpace(f.Port))
	if err != nil || port < 1 || port > 65535 {
		return fail(FieldPort, "settings.err_port")
	}
	cfg.Port = port
	if key := strings.TrimSpace(f.Key); key != "" {
		if espapi.ValidateKey(key) != nil {
			return fail(FieldKey, "settings.err_key")
		}
		if err := cfg.SetKey(key); err != nil {
			return saved, err
		}
	} else if f.RemoveKey {
		cfg.SetKey("")
	}
	cfg.KeyPlain = ""
	cfg.PCName = strings.TrimSpace(f.PCName)
	cfg.Language = m.knownLanguage(f.Language)
	cfg.SendAppNames, cfg.Doorbell, cfg.Discord = f.SendApps, f.Doorbell, f.Discord
	if slices.Contains(config.NotifyModes, f.Notify) {
		cfg.Notify = f.Notify
	}
	cfg.Ignore = nil
	for _, line := range strings.Split(f.Ignore, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			cfg.Ignore = append(cfg.Ignore, line)
		}
	}
	cfg.AppNames = nil
	for i, line := range strings.Split(f.Names, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		exe, name, ok := strings.Cut(line, "=")
		exe, name = strings.TrimSpace(exe), strings.TrimSpace(name)
		if !ok || exe == "" || name == "" {
			return fail(FieldNames, "settings.err_names", "line", strconv.Itoa(i+1))
		}
		if cfg.AppNames == nil {
			cfg.AppNames = map[string]string{}
		}
		cfg.AppNames[exe] = name
	}
	return cfg, nil
}

func (m *Model) knownLanguage(code string) string {
	if slices.Contains(m.deps.Catalog.Languages(), code) {
		return code
	}
	return ""
}

// Save validates the form, writes the configuration (the tray reloads it)
// and applies "Start with Windows". A *FieldError names the invalid field.
func (m *Model) Save(f Form) error {
	cfg, err := m.apply(f)
	if err != nil {
		return err
	}
	if err := config.Save(m.deps.ConfigPath, cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	if f.Autostart != m.deps.Autostart.Enabled() {
		if err := m.deps.Autostart.Set(f.Autostart); err != nil {
			return fmt.Errorf("settingsui: start with Windows: %w", err)
		}
	}
	m.deps.Logger.Info("settings saved")
	return nil
}

// SaveError returns the message shown when Save fails.
func (m *Model) SaveError(err error) string {
	return m.T("settings.error", "error", err.Error())
}

// Test tries the connection with the form's address and key (the saved key
// when the field is empty) and returns a localized outcome. It takes up to
// 10 s and is safe to call from any goroutine: it reads the model only.
func (m *Model) Test(ctx context.Context, f Form) (ok bool, message string) {
	cfg, err := m.apply(f)
	if err != nil {
		return false, err.Error()
	}
	key, err := cfg.Key()
	if err != nil {
		return false, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := m.deps.Test(ctx, espapi.Options{Address: cfg.Address(), EncryptionKey: key, ClientInfo: m.deps.ClientInfo})
	switch {
	case err == nil:
		return true, m.T("settings.test_ok", "device", res.Device, "version", res.Version)
	case errors.Is(err, espapi.ErrInvalidKey), errors.Is(err, espapi.ErrInvalidKeyFormat):
		return false, m.T("settings.test_bad_key")
	case errors.Is(err, espapi.ErrRequiresEncryption):
		return false, m.T("settings.test_needs_key")
	case errors.Is(err, espapi.ErrDevicePlaintext):
		return false, m.T("settings.test_no_encryption")
	case errors.Is(err, link.ErrNoPresenceAction):
		return false, m.T("settings.test_old_firmware")
	default:
		m.deps.Logger.Info("connection test failed", "err", err)
		return false, m.T("settings.test_unreachable", "address", cfg.Address())
	}
}

// espapiTest connects, checks the device has the presence action, and
// disconnects.
func espapiTest(ctx context.Context, opts espapi.Options) (TestResult, error) {
	c, err := espapi.Dial(ctx, opts)
	if err != nil {
		return TestResult{}, err
	}
	defer c.Close()
	ents, err := c.ListEntities(ctx)
	if err != nil {
		return TestResult{}, err
	}
	if _, ok := ents.Find(espapi.KindAction, "report_presence"); !ok {
		return TestResult{}, link.ErrNoPresenceAction
	}
	return TestResult{Device: c.DeviceName(), Version: c.ServerInfo()}, nil
}
