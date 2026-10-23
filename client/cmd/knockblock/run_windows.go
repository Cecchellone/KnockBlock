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

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/logfile"
	"github.com/Cecchellone/KnockBlock/client/internal/memdebug"
	"github.com/Cecchellone/KnockBlock/client/internal/tray"
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
	return tray.Run(tray.Options{
		ConfigPath:   cfgPath,
		Version:      version,
		Logger:       log,
		OpenSettings: openConfigFile,
	})
}

// openConfigFile opens config.json in Notepad, creating it with the
// defaults first. The settings page (milestone 7) replaces this.
func openConfigFile(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := config.Save(path, config.Default()); err != nil {
			return err
		}
	}
	verb, _ := windows.UTF16PtrFromString("open")
	exe, _ := windows.UTF16PtrFromString("notepad.exe")
	arg, _ := windows.UTF16PtrFromString(`"` + path + `"`)
	return windows.ShellExecute(0, verb, exe, arg, nil, windows.SW_SHOWNORMAL)
}
