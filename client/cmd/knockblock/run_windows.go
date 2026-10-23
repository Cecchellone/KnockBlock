//go:build windows

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Cecchellone/KnockBlock/client/internal/autostart"
	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/internal/logfile"
	"github.com/Cecchellone/KnockBlock/client/internal/memdebug"
	"github.com/Cecchellone/KnockBlock/client/internal/settingsui"
	"github.com/Cecchellone/KnockBlock/client/internal/tray"
	"github.com/Cecchellone/KnockBlock/client/locales"
)

func run(debugLog bool, debugMem time.Duration) error {
	// One instance per user session: autostart plus a manual start must
	// not show two icons.
	name, _ := windows.UTF16PtrFromString(`Local\KnockBlockTray`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if err == nil {
		defer windows.CloseHandle(mutex)
	}

	// Keep the heap small (CLAUDE.md §2.1): a soft limit makes the GC work
	// harder before the process grows. GOMEMLIMIT overrides it.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(10 << 20)
	}

	logDir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		return err
	}
	logs, err := logfile.Open(filepath.Join(logDir, "KnockBlock", "logs", "knockblock.log"), 1<<20, 2)
	if err != nil {
		return err
	}
	defer logs.Close()
	level := slog.LevelInfo
	if debugLog {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level}))
	log.Info("starting", "version", version)

	if debugMem > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go memdebug.Run(ctx, log, debugMem)
	}

	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	catalog, err := i18n.Load(locales.FS)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	settings := settingsui.New(settingsui.Deps{
		ConfigPath:      cfgPath,
		Catalog:         catalog,
		SystemLanguages: i18n.SystemLanguages,
		Hostname:        hostname,
		Autostart:       autostartSetting{},
		Browse:          browse,
		Logger:          log,
		ClientInfo:      "KnockBlock " + version + " (settings)",
	})
	defer settings.Close()
	return tray.Run(tray.Options{
		ConfigPath:   cfgPath,
		Version:      version,
		Logger:       log,
		OpenSettings: func(string) error { return settings.Open() },
	})
}

// autostartSetting adapts the autostart package to the settings page.
type autostartSetting struct{}

func (autostartSetting) Enabled() bool     { return autostart.Enabled() }
func (autostartSetting) Set(on bool) error { return autostart.Set(on) }

// browse opens url in the default browser.
func browse(url string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(url)
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
