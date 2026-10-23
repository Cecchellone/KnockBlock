//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
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

func run(o options) error {
	// One instance per user session: autostart plus a manual start must
	// not show two icons. The settings smoke test runs beside the tray.
	if o.settingsSmoke == "" {
		name, _ := windows.UTF16PtrFromString(`Local\KnockBlockTray`)
		mutex, err := windows.CreateMutex(nil, false, name)
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil
		}
		if err == nil {
			defer windows.CloseHandle(mutex)
		}
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
	if o.debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level}))
	log.Info("starting", "version", version)

	if o.debugMem > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go memdebug.Run(ctx, log, o.debugMem)
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
	settings := settingsui.NewWindow(settingsui.Deps{
		ConfigPath:      cfgPath,
		Catalog:         catalog,
		SystemLanguages: i18n.SystemLanguages,
		Hostname:        hostname,
		Autostart:       autostartSetting{},
		Logger:          log,
		ClientInfo:      "KnockBlock " + version + " (settings)",
	})
	defer settings.Close()
	if o.settingsSmoke != "" {
		return settingsSmoke(settings, o.settingsSmoke, log)
	}
	return tray.Run(tray.Options{
		ConfigPath:   cfgPath,
		Version:      version,
		Logger:       log,
		OpenSettings: func(string) error { return settings.Open() },
	})
}

// settingsSmoke opens the settings window, checks it has its controls,
// saves a screenshot of every tab (FILE-1.png, FILE-2.png, …, from FILE
// without its extension) and logs the memory used while open, then closes
// it.
func settingsSmoke(w *settingsui.Window, file string, log *slog.Logger) error {
	if err := w.Open(); err != nil {
		log.Error("settings smoke test", "err", err)
		return err
	}
	time.Sleep(4 * time.Second) // let it paint and give up on the light
	base := strings.TrimSuffix(file, filepath.Ext(file))
	var controls int
	var err error
	for i := range settingsui.Tabs {
		w.SelectTab(i)
		time.Sleep(300 * time.Millisecond)
		var out *os.File
		if out, err = os.Create(fmt.Sprintf("%s-%d.png", base, i+1)); err != nil {
			break
		}
		controls, err = w.Capture(out)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			break
		}
	}
	runtime.GC()
	memdebug.Log(log)
	w.Close()
	log.Info("settings smoke test", "controls", controls, "screenshots", base+"-N.png", "err", err)
	if err == nil && controls < 30 {
		err = fmt.Errorf("settings window has %d controls", controls)
	}
	return err
}

// autostartSetting adapts the autostart package to the settings window.
type autostartSetting struct{}

func (autostartSetting) Enabled() bool     { return autostart.Enabled() }
func (autostartSetting) Set(on bool) error { return autostart.Set(on) }
