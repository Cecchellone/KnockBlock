package settingsui

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
)

//go:embed page.html
var pageHTML string

// DefaultIdleTimeout closes the server when the page has not been used or
// pinged for this long; an open page pings every 30 s.
const DefaultIdleTimeout = 3 * time.Minute

// Autostart reads and changes "Start with Windows".
type Autostart interface {
	Enabled() bool
	Set(enabled bool) error
}

// TestResult is the outcome of a connection test.
type TestResult struct {
	Device  string // device name
	Version string // ESPHome version
}

// Deps configures the settings server.
type Deps struct {
	ConfigPath string
	Catalog    *i18n.Catalog
	// SystemLanguages returns the Windows UI languages (i18n.SystemLanguages).
	SystemLanguages func() []string
	Hostname        string // default PC name
	Autostart       Autostart
	// Browse opens a URL in the default browser.
	Browse func(url string) error
	// Test connects to a device with the given options. Default: espapi.
	Test        func(ctx context.Context, opts espapi.Options) (TestResult, error)
	IdleTimeout time.Duration
	Logger      *slog.Logger
	ClientInfo  string
}

// Server serves the settings page on 127.0.0.1, only while it is open
// (CLAUDE.md §2.1, §5.4). Open starts it on demand; it stops by itself
// after IdleTimeout without requests.
type Server struct {
	deps Deps

	mu      sync.Mutex
	srv     *http.Server
	url     string
	lastUse time.Time
}

// New returns a stopped server.
func New(d Deps) *Server {
	if d.IdleTimeout <= 0 {
		d.IdleTimeout = DefaultIdleTimeout
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Test == nil {
		d.Test = espapiTest
	}
	if d.SystemLanguages == nil {
		d.SystemLanguages = func() []string { return nil }
	}
	return &Server{deps: d}
}

// Open starts the server if needed and opens the page in the browser.
func (s *Server) Open() error {
	s.mu.Lock()
	if s.srv == nil {
		if err := s.start(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.lastUse = time.Now()
	url := s.url
	s.mu.Unlock()
	return s.deps.Browse(url)
}

// start listens on a random loopback port with a fresh token. Caller holds
// s.mu.
func (s *Server) start() error {
	token, err := newToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("settingsui: %w", err)
	}
	host := ln.Addr().String()
	srv := &http.Server{
		Handler:           s.handler(token, host),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.srv, s.url = srv, "http://"+host+"/"+token+"/"
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.deps.Logger.Error("settings server", "err", err)
		}
	}()
	go s.closeWhenIdle(srv)
	s.deps.Logger.Info("settings page open", "addr", host)
	return nil
}

func (s *Server) closeWhenIdle(srv *http.Server) {
	tick := time.NewTicker(min(s.deps.IdleTimeout/4, 15*time.Second))
	defer tick.Stop()
	for range tick.C {
		s.mu.Lock()
		if s.srv != srv {
			s.mu.Unlock()
			return
		}
		idle := time.Since(s.lastUse) >= s.deps.IdleTimeout
		if idle {
			s.srv = nil
		}
		s.mu.Unlock()
		if idle {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			srv.Shutdown(ctx)
			cancel()
			s.deps.Logger.Info("settings page closed after inactivity")
			return
		}
	}
}

// Close stops the server now.
func (s *Server) Close() {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}

// Running reports whether the server is up.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handler serves the page under /<token>/. Requests must carry the exact
// loopback Host, which blocks DNS-rebinding attacks from web pages.
func (s *Server) handler(token, host string) http.Handler {
	page := template.Must(template.New("page").Funcs(template.FuncMap{"t": func(string, ...string) string { return "" }}).Parse(pageHTML))
	mux := http.NewServeMux()
	base := "/" + token
	mux.HandleFunc("GET "+base+"/{$}", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.Load(s.deps.ConfigPath)
		data := s.pageData(cfg, token)
		if err != nil {
			data.Error = err.Error()
		}
		s.render(w, page, cfg, data)
	})
	mux.HandleFunc("POST "+base+"/save", func(w http.ResponseWriter, r *http.Request) { s.save(w, r, page, token) })
	mux.HandleFunc("POST "+base+"/test", s.test)
	mux.HandleFunc("POST "+base+"/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.mu.Lock()
		s.lastUse = time.Now()
		s.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}

// translator picks the page language like the tray does.
func (s *Server) translator(cfg config.Config) *i18n.Translator {
	prefs := s.deps.SystemLanguages()
	if cfg.Language != "" {
		prefs = append([]string{cfg.Language}, prefs...)
	}
	return s.deps.Catalog.Translator(s.deps.Catalog.Match(prefs...))
}

type language struct{ Code, Name string }

type pageData struct {
	Lang, Token             string
	Host                    string
	Port                    int
	KeySaved                bool
	PCName, DefaultPC       string
	Language                string
	DisplayLanguage         string
	Languages               []language
	SendApps, Notifications bool
	Autostart               bool
	Ignore, Names           string
	Message, Error          string
	Minutes                 string // for the "t" function, which takes strings
}

func (s *Server) pageData(cfg config.Config, token string) pageData {
	var langs []language
	for _, code := range s.deps.Catalog.Languages() {
		name := s.deps.Catalog.Messages(code)["lang.name"]
		if name == "" {
			name = code
		}
		langs = append(langs, language{code, name})
	}
	names := make([]string, 0, len(cfg.AppNames))
	for _, exe := range slices.Sorted(maps.Keys(cfg.AppNames)) {
		names = append(names, exe+" = "+cfg.AppNames[exe])
	}
	return pageData{
		Token: token, Host: cfg.Host, Port: cfg.Port, KeySaved: cfg.KeyProtected != "",
		PCName: cfg.PCName, DefaultPC: s.deps.Hostname,
		Language: cfg.Language, DisplayLanguage: cfg.DisplayLanguage, Languages: langs,
		SendApps: cfg.SendAppNames, Notifications: cfg.Notifications, Autostart: s.deps.Autostart.Enabled(),
		Ignore: strings.Join(cfg.Ignore, "\n"), Names: strings.Join(names, "\n"),
		Minutes: strconv.Itoa(max(1, int(s.deps.IdleTimeout/time.Minute))),
	}
}

func (s *Server) render(w http.ResponseWriter, page *template.Template, cfg config.Config, data pageData) {
	tr := s.translator(cfg)
	data.Lang = tr.Lang
	p, err := page.Clone()
	if err == nil {
		p.Funcs(template.FuncMap{"t": tr.T})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		err = p.Execute(w, data)
	}
	if err != nil {
		s.deps.Logger.Error("rendering settings page", "err", err)
	}
}

// formError is a validation error with a translated message.
type formError struct {
	key  string
	args []string
}

func (e formError) Error() string { return e.key }

// parseForm builds the new configuration from the form, starting from the
// current one so fields the page does not show are kept.
func (s *Server) parseForm(r *http.Request, cur config.Config) (config.Config, error) {
	cfg := cur
	cfg.Host = strings.TrimSpace(r.PostFormValue("host"))
	if cfg.Host == "" {
		return cur, formError{key: "settings.err_host"}
	}
	port, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("port")))
	if err != nil || port < 1 || port > 65535 {
		return cur, formError{key: "settings.err_port"}
	}
	cfg.Port = port
	if key := strings.TrimSpace(r.PostFormValue("key")); key != "" {
		if espapi.ValidateKey(key) != nil {
			return cur, formError{key: "settings.err_key"}
		}
		if err := cfg.SetKey(key); err != nil {
			return cur, err
		}
	} else if r.PostFormValue("remove_key") != "" {
		cfg.SetKey("")
	}
	cfg.KeyPlain = ""
	cfg.PCName = strings.TrimSpace(r.PostFormValue("pc_name"))
	cfg.Language = s.knownLanguage(r.PostFormValue("language"))
	cfg.DisplayLanguage = s.knownLanguage(r.PostFormValue("display_language"))
	cfg.SendAppNames = r.PostFormValue("send_apps") != ""
	cfg.Notifications = r.PostFormValue("notifications") != ""
	cfg.Ignore = nil
	for _, line := range strings.Split(r.PostFormValue("ignore"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			cfg.Ignore = append(cfg.Ignore, line)
		}
	}
	cfg.AppNames = nil
	for i, line := range strings.Split(r.PostFormValue("names"), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		exe, name, ok := strings.Cut(line, "=")
		exe, name = strings.TrimSpace(exe), strings.TrimSpace(name)
		if !ok || exe == "" || name == "" {
			return cur, formError{key: "settings.err_names", args: []string{"line", strconv.Itoa(i + 1)}}
		}
		if cfg.AppNames == nil {
			cfg.AppNames = map[string]string{}
		}
		cfg.AppNames[exe] = name
	}
	return cfg, nil
}

func (s *Server) knownLanguage(code string) string {
	if slices.Contains(s.deps.Catalog.Languages(), code) {
		return code
	}
	return ""
}

func (s *Server) save(w http.ResponseWriter, r *http.Request, page *template.Template, token string) {
	cur, _ := config.Load(s.deps.ConfigPath)
	cfg, err := s.parseForm(r, cur)
	if err == nil {
		err = config.Save(s.deps.ConfigPath, cfg)
	}
	if err == nil {
		if want := r.PostFormValue("autostart") != ""; want != s.deps.Autostart.Enabled() {
			err = s.deps.Autostart.Set(want)
		}
	}
	shown := cfg
	if err != nil {
		shown = cur
	}
	data := s.pageData(shown, token)
	tr := s.translator(shown)
	var fe formError
	switch {
	case err == nil:
		data.Message = tr.T("settings.saved")
		s.deps.Logger.Info("settings saved")
	case errors.As(err, &fe):
		data.Error = tr.T("settings.error", "error", tr.T(fe.key, fe.args...))
	default:
		data.Error = tr.T("settings.error", "error", err.Error())
	}
	s.render(w, page, shown, data)
}

// test tries the connection with the form's address and key (or the saved
// key when the field is empty).
func (s *Server) test(w http.ResponseWriter, r *http.Request) {
	cur, _ := config.Load(s.deps.ConfigPath)
	tr := s.translator(cur)
	reply := func(ok bool, msg string) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": ok, "message": msg})
	}
	cfg, err := s.parseForm(r, cur)
	if err != nil {
		var fe formError
		if errors.As(err, &fe) {
			reply(false, tr.T(fe.key, fe.args...))
		} else {
			reply(false, err.Error())
		}
		return
	}
	key, err := cfg.Key()
	if err != nil {
		reply(false, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	res, err := s.deps.Test(ctx, espapi.Options{Address: cfg.Address(), EncryptionKey: key, ClientInfo: s.deps.ClientInfo})
	switch {
	case err == nil:
		reply(true, tr.T("settings.test_ok", "device", res.Device, "version", res.Version))
	case errors.Is(err, espapi.ErrInvalidKey), errors.Is(err, espapi.ErrInvalidKeyFormat):
		reply(false, tr.T("settings.test_bad_key"))
	case errors.Is(err, espapi.ErrRequiresEncryption):
		reply(false, tr.T("settings.test_needs_key"))
	case errors.Is(err, espapi.ErrDevicePlaintext):
		reply(false, tr.T("settings.test_no_encryption"))
	case errors.Is(err, link.ErrNoPresenceAction):
		reply(false, tr.T("settings.test_old_firmware"))
	default:
		s.deps.Logger.Info("connection test failed", "err", err)
		reply(false, tr.T("settings.test_unreachable", "address", cfg.Address()))
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
