package settingsui

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/locales"
)

const (
	testToken = "tok123"
	testHost  = "127.0.0.1:5555"
	validKey  = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
)

type fakeAutostart struct{ on bool }

func (f *fakeAutostart) Enabled() bool     { return f.on }
func (f *fakeAutostart) Set(on bool) error { f.on = on; return nil }

func newTestServer(t *testing.T, test func(context.Context, espapi.Options) (TestResult, error)) (*Server, http.Handler, string, *fakeAutostart) {
	t.Helper()
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	auto := &fakeAutostart{}
	s := New(Deps{
		ConfigPath: path, Catalog: cat, Hostname: "DESKTOP-1", Autostart: auto,
		SystemLanguages: func() []string { return []string{"it-IT"} },
		Browse:          func(string) error { return nil },
		Test:            test,
	})
	return s, s.handler(testToken, testHost), path, auto
}

func do(h http.Handler, method, path string, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, body)
	r.Host = testHost
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func validForm() url.Values {
	return url.Values{
		"host": {"luce.lan"}, "port": {"6053"}, "key": {validKey},
		"pc_name": {"STUDIO"}, "language": {"en"}, "display_language": {"it"},
		"send_apps": {"1"}, "autostart": {"1"}, "doorbell": {"1"}, "display_flip": {"on"},
		"ignore": {"obs64.exe\n\n nvidia broadcast.exe "}, "names": {"rec.exe = Recorder\n"},
	}
}

func TestAccessControl(t *testing.T) {
	_, h, _, _ := newTestServer(t, nil)
	if w := do(h, "GET", "/"+testToken+"/", nil); w.Code != 200 {
		t.Fatalf("page: %d", w.Code)
	}
	if w := do(h, "GET", "/wrong/", nil); w.Code != 404 {
		t.Errorf("wrong token: %d, want 404", w.Code)
	}
	if w := do(h, "GET", "/", nil); w.Code != 404 {
		t.Errorf("root: %d, want 404", w.Code)
	}
	r := httptest.NewRequest("GET", "/"+testToken+"/", nil)
	r.Host = "evil.example:5555" // DNS rebinding
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("foreign Host: %d, want 403", w.Code)
	}
	if w := do(h, "GET", "/"+testToken+"/save", nil); w.Code != 405 {
		t.Errorf("GET save: %d, want 405", w.Code)
	}
}

func TestPageIsLocalizedAndHidesKey(t *testing.T) {
	_, h, path, _ := newTestServer(t, nil)
	cfg := config.Default()
	cfg.SetKey(validKey)
	config.Save(path, cfg)
	body := html.UnescapeString(do(h, "GET", "/"+testToken+"/", nil).Body.String())
	// The whole page renders: footer and script come last.
	for _, want := range []string{`lang="it"`, "Impostazioni di KnockBlock", "È salvata una chiave.", "DESKTOP-1", "knockblock.local",
		"si chiude dopo 3 minuti", `"Prova in corso…"`, "</html>"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, validKey) || strings.Contains(body, cfg.KeyProtected) {
		t.Error("page contains the key")
	}
}

func TestSave(t *testing.T) {
	_, h, path, auto := newTestServer(t, nil)
	w := do(h, "POST", "/"+testToken+"/save", validForm())
	// The page switches to the newly chosen language.
	if !strings.Contains(w.Body.String(), "Saved. KnockBlock is using the new settings.") {
		t.Fatalf("no confirmation in %q", w.Body.String())
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := cfg.Key()
	if cfg.Host != "luce.lan" || cfg.PCName != "STUDIO" || cfg.Language != "en" || cfg.DisplayLanguage != "it" ||
		!cfg.SendAppNames || cfg.Notifications || !cfg.Doorbell || cfg.DisplayFlip != "on" || key != validKey {
		t.Errorf("saved %+v (key %q)", cfg, key)
	}
	if len(cfg.Ignore) != 2 || cfg.Ignore[1] != "nvidia broadcast.exe" || cfg.AppNames["rec.exe"] != "Recorder" {
		t.Errorf("lists %v %v", cfg.Ignore, cfg.AppNames)
	}
	if !auto.on {
		t.Error("autostart not enabled")
	}

	// An empty key field keeps the saved key; remove_key deletes it.
	f := validForm()
	f.Set("key", "")
	do(h, "POST", "/"+testToken+"/save", f)
	if c, _ := config.Load(path); c.KeyProtected == "" {
		t.Error("empty key field removed the key")
	}
	f.Set("remove_key", "1")
	do(h, "POST", "/"+testToken+"/save", f)
	if c, _ := config.Load(path); c.KeyProtected != "" {
		t.Error("remove_key kept the key")
	}
}

func TestSaveValidation(t *testing.T) {
	tests := []struct {
		field, value, want string
	}{
		{"host", " ", "l'indirizzo è vuoto"},
		{"port", "70000", "la porta deve essere tra 1 e 65535"},
		{"key", "not-a-key", "la chiave deve essere quella in base64"},
		{"names", "ok.exe = Ok\nbroken line", "la riga 2 dei nomi"},
	}
	for _, tt := range tests {
		_, h, path, _ := newTestServer(t, nil)
		f := validForm()
		f.Set("language", "") // keep the Italian page
		f.Set(tt.field, tt.value)
		body := html.UnescapeString(do(h, "POST", "/"+testToken+"/save", f).Body.String())
		if !strings.Contains(body, "Non salvato") || !strings.Contains(body, tt.want) {
			t.Errorf("%s=%q: page lacks %q", tt.field, tt.value, tt.want)
		}
		if c, _ := config.Load(path); c.Host != config.DefaultHost {
			t.Errorf("%s=%q: invalid form was saved", tt.field, tt.value)
		}
	}
}

func TestConnectionTest(t *testing.T) {
	tests := []struct {
		err  error
		ok   bool
		want string
	}{
		{nil, true, "Connesso a knockblock (ESPHome 2026.10.0)."},
		{espapi.ErrInvalidKey, false, "La luce ha rifiutato la chiave"},
		{espapi.ErrRequiresEncryption, false, "richiede una chiave"},
		{context.DeadlineExceeded, false, "non è raggiungibile all'indirizzo luce.lan:6053"},
	}
	for _, tt := range tests {
		var got espapi.Options
		_, h, _, _ := newTestServer(t, func(_ context.Context, o espapi.Options) (TestResult, error) {
			got = o
			return TestResult{Device: "knockblock", Version: "2026.10.0"}, tt.err
		})
		f := validForm()
		f.Set("language", "")
		w := do(h, "POST", "/"+testToken+"/test", f)
		var reply struct {
			OK      bool
			Message string
		}
		json.Unmarshal(w.Body.Bytes(), &reply)
		if reply.OK != tt.ok || !strings.Contains(reply.Message, tt.want) {
			t.Errorf("%v: reply %+v, want %q", tt.err, reply, tt.want)
		}
		if got.Address != "luce.lan:6053" || got.EncryptionKey != validKey {
			t.Errorf("tested with %+v", got)
		}
	}
}

func TestOpenAndIdleClose(t *testing.T) {
	cat, _ := i18n.Load(locales.FS)
	var opened []string
	s := New(Deps{
		ConfigPath: filepath.Join(t.TempDir(), "c.json"), Catalog: cat, Autostart: &fakeAutostart{},
		Browse: func(u string) error { opened = append(opened, u); return nil }, IdleTimeout: 200 * time.Millisecond,
	})
	defer s.Close()
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(); err != nil { // second click reuses the server
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[0] != opened[1] || !strings.HasPrefix(opened[0], "http://127.0.0.1:") {
		t.Fatalf("opened %v", opened)
	}
	resp, err := http.Get(opened[0])
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET page: %v %v", resp, err)
	}
	resp.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for s.Running() {
		if time.Now().After(deadline) {
			t.Fatal("server still running after the idle timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := http.Get(opened[0]); err == nil {
		t.Error("server still answers after closing")
	}
}
