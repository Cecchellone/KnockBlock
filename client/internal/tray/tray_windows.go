//go:build windows

package tray

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/Cecchellone/KnockBlock/client/internal/autostart"
	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/internal/icon"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
	"github.com/Cecchellone/KnockBlock/client/internal/status"
	"github.com/Cecchellone/KnockBlock/client/internal/sysevents"
	"github.com/Cecchellone/KnockBlock/client/locales"
)

// Run shows the tray icon and runs the app until the user quits.
func Run(opts Options) error {
	a := &app{opts: opts, log: opts.Logger, poke: make(chan struct{}, 1), quit: make(chan struct{}), icons: map[color.NRGBA][]byte{}}
	systray.Run(a.onReady, a.onExit)
	return nil
}

type app struct {
	opts    Options
	log     *slog.Logger
	catalog *i18n.Catalog
	engine  *Engine
	alerts  Alerts
	icons   map[color.NRGBA][]byte
	poke    chan struct{}
	quit    chan struct{}

	mu     sync.Mutex
	cfg    config.Config
	cfgMod time.Time
	tr     *i18n.Translator
	snap   Snapshot
	shown  View

	items struct {
		status, conn, auto, free, dnd, settings, autostart, quit *systray.MenuItem
	}
	stopEvents func()
}

func (a *app) onReady() {
	var err error
	if a.catalog, err = i18n.Load(locales.FS); err != nil {
		a.log.Error("loading translations", "err", err)
	}
	a.loadConfig()
	systray.SetIcon(a.iconFor(Disconnected))
	systray.SetTitle("KnockBlock")
	a.buildMenu()

	a.engine = NewEngine(Deps{
		NewDetector: detect.NewConsentStore,
		Dial:        link.DialESPHome,
		OnResult:    func(r status.Result) { a.update(func(s *Snapshot) { s.Result = r }) },
		OnState:     func(st link.State) { a.update(func(s *Snapshot) { s.Link = st }) },
		Logger:      a.log,
		ClientInfo:  "KnockBlock " + a.opts.Version,
	})
	a.startEngine()
	a.stopEvents, _ = sysevents.Watch(func(reason string) {
		a.log.Info("reconnecting", "reason", reason)
		a.engine.ForceReconnect()
	})
	go a.uiLoop()
	go a.menuLoop()

	// First start: nothing is configured yet, so show the settings page
	// instead of a grey icon the user may not notice.
	if _, err := os.Stat(a.opts.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		a.openSettings()
	}
}

func (a *app) onExit() {
	close(a.quit)
	if a.stopEvents != nil {
		a.stopEvents()
	}
	if a.engine != nil {
		a.engine.Stop() // reports offline
	}
	a.log.Info("exited")
}

// loadConfig reads the configuration and picks the language.
func (a *app) loadConfig() {
	cfg, err := config.Load(a.opts.ConfigPath)
	if err != nil {
		a.log.Error("loading configuration; using defaults", "err", err)
	}
	// A key pasted into config.json by hand is encrypted and removed.
	if changed, err := cfg.ProtectPlainKey(); err != nil {
		a.log.Error("protecting the key", "err", err)
	} else if changed {
		if err := config.Save(a.opts.ConfigPath, cfg); err != nil {
			a.log.Error("saving the protected key", "err", err)
		}
	}
	var mod time.Time
	if fi, err := os.Stat(a.opts.ConfigPath); err == nil {
		mod = fi.ModTime()
	}
	lang := i18n.Fallback
	if a.catalog != nil {
		prefs := i18n.SystemLanguages()
		if cfg.Language != "" {
			prefs = append([]string{cfg.Language}, prefs...)
		}
		lang = a.catalog.Match(prefs...)
	}
	a.mu.Lock()
	a.cfg, a.cfgMod = cfg, mod
	if a.catalog != nil {
		a.tr = a.catalog.Translator(lang)
	}
	a.snap.Device = cfg.Host
	a.shown = View{} // force a redraw
	a.mu.Unlock()
}

func (a *app) startEngine() {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if err := a.engine.Start(cfg); err != nil {
		a.log.Error("starting", "err", err)
		a.update(func(s *Snapshot) { s.Link = link.State{Err: err, NeedsConfig: true} })
	}
}

func (a *app) t(key string, args ...string) string {
	a.mu.Lock()
	tr := a.tr
	a.mu.Unlock()
	if tr == nil {
		return key
	}
	return tr.T(key, args...)
}

func (a *app) buildMenu() {
	a.items.status = systray.AddMenuItem("", "")
	a.items.status.Disable()
	a.items.conn = systray.AddMenuItem("", "")
	a.items.conn.Disable()
	systray.AddSeparator()
	a.items.auto = systray.AddMenuItemCheckbox("", "", true)
	a.items.free = systray.AddMenuItemCheckbox("", "", false)
	a.items.dnd = systray.AddMenuItemCheckbox("", "", false)
	systray.AddSeparator()
	a.items.settings = systray.AddMenuItem("", "")
	a.items.autostart = systray.AddMenuItemCheckbox("", "", autostart.Enabled())
	systray.AddSeparator()
	a.items.quit = systray.AddMenuItem("", "")
	a.setMenuTitles()
}

func (a *app) setMenuTitles() {
	a.items.auto.SetTitle(a.t("menu.auto"))
	a.items.free.SetTitle(a.t("menu.free"))
	a.items.dnd.SetTitle(a.t("menu.dnd"))
	a.items.settings.SetTitle(a.t("menu.settings"))
	a.items.autostart.SetTitle(a.t("menu.autostart"))
	a.items.quit.SetTitle(a.t("menu.quit"))
}

func (a *app) menuLoop() {
	for {
		select {
		case <-a.quit:
			return
		case <-a.items.auto.ClickedCh:
			a.setOverride(status.Auto)
		case <-a.items.free.ClickedCh:
			a.setOverride(status.ForceFree)
		case <-a.items.dnd.ClickedCh:
			a.setOverride(status.ForceDND)
		case <-a.items.settings.ClickedCh:
			a.openSettings()
		case <-a.items.autostart.ClickedCh:
			on := !a.items.autostart.Checked()
			if err := autostart.Set(on); err != nil {
				a.log.Error("changing autostart", "err", err)
				continue
			}
			if on {
				a.items.autostart.Check()
			} else {
				a.items.autostart.Uncheck()
			}
		case <-a.items.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (a *app) openSettings() {
	if a.opts.OpenSettings == nil {
		return
	}
	if err := a.opts.OpenSettings(a.opts.ConfigPath); err != nil {
		a.log.Error("opening settings", "err", err)
	}
}

func (a *app) setOverride(o status.Override) {
	a.engine.SetOverride(o)
	for item, v := range map[*systray.MenuItem]status.Override{a.items.auto: status.Auto, a.items.free: status.ForceFree, a.items.dnd: status.ForceDND} {
		if v == o {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

// update changes the snapshot and asks the UI loop to redraw.
func (a *app) update(fn func(*Snapshot)) {
	a.mu.Lock()
	fn(&a.snap)
	a.mu.Unlock()
	select {
	case a.poke <- struct{}{}:
	default:
	}
}

// uiLoop redraws on changes, checks alerts as time passes, and reloads the
// configuration when its file changes (the settings page writes it).
func (a *app) uiLoop() {
	alertTick := time.NewTicker(30 * time.Second)
	defer alertTick.Stop()
	cfgTick := time.NewTicker(2 * time.Second)
	defer cfgTick.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-a.poke:
			a.redraw()
			a.checkAlerts()
		case <-alertTick.C:
			a.checkAlerts()
		case <-cfgTick.C:
			if a.configChanged() {
				a.log.Info("configuration changed; restarting")
				a.loadConfig()
				a.setMenuTitles()
				a.startEngine()
				a.redraw()
			}
		}
	}
}

func (a *app) configChanged() bool {
	fi, err := os.Stat(a.opts.ConfigPath)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return !fi.ModTime().Equal(a.cfgMod)
}

func (a *app) redraw() {
	a.mu.Lock()
	tr, snap, shown := a.tr, a.snap, a.shown
	a.mu.Unlock()
	if tr == nil {
		return
	}
	v := Render(tr, snap)
	if v == shown {
		return
	}
	if v.Color != shown.Color {
		systray.SetIcon(a.iconFor(v.Color))
	}
	systray.SetTooltip(v.Tooltip)
	a.items.status.SetTitle(tr.T("menu.status", "status", v.Status))
	a.items.conn.SetTitle(v.Connection)
	a.mu.Lock()
	a.shown = v
	a.mu.Unlock()
}

func (a *app) checkAlerts() {
	a.mu.Lock()
	st, notifications := a.snap.Link, a.cfg.Notifications
	a.mu.Unlock()
	alert, ok := a.alerts.Update(st, time.Now())
	if !ok || !notifications {
		return
	}
	if err := showNotification(a.t("app.name"), a.t(alert.Key, alert.Args...)); err != nil {
		a.log.Warn("notification not shown", "err", err)
	}
}

// iconFor returns the tray icon in a status colour, drawn once per colour.
func (a *app) iconFor(c color.NRGBA) []byte {
	if ico, ok := a.icons[c]; ok {
		return ico
	}
	var buf bytes.Buffer
	if err := icon.EncodeICO(&buf, []*image.NRGBA{icon.Render(16, c), icon.Render(24, c), icon.Render(32, c)}); err != nil {
		a.log.Error("drawing icon", "err", err)
	}
	a.icons[c] = buf.Bytes()
	return a.icons[c]
}
