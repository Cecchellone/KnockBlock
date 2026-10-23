package settingsui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/locales"
)

const validKey = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="

type fakeAutostart struct{ on bool }

func (f *fakeAutostart) Enabled() bool     { return f.on }
func (f *fakeAutostart) Set(on bool) error { f.on = on; return nil }

// newModel loads a model over a fresh config file, with Windows in Italian.
func newModel(t *testing.T, test func(context.Context, espapi.Options) (TestResult, error)) (*Model, string, *fakeAutostart) {
	t.Helper()
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	auto := &fakeAutostart{}
	m := Load(Deps{
		ConfigPath: path, Catalog: cat, Hostname: "DESKTOP-1", Autostart: auto,
		SystemLanguages: func() []string { return []string{"it-IT"} },
		Test:            test,
	})
	return m, path, auto
}

func validForm() Form {
	return Form{
		Host: " luce.lan ", Port: "6053", Key: validKey,
		PCName: "STUDIO", Language: "en",
		SendApps: true, Doorbell: true, Autostart: true,
		Ignore: "obs64.exe\n\n nvidia broadcast.exe ", Names: "rec.exe = Recorder\n",
	}
}

func TestLocalizedChoices(t *testing.T) {
	m, _, _ := newModel(t, nil)
	if got := m.T("settings.title"); got != "Impostazioni di KnockBlock" {
		t.Errorf("title %q: the window must follow Windows (Italian)", got)
	}
	langs := m.Languages("settings.language_auto")
	if len(langs) < 3 || langs[0] != (Choice{"", "Come Windows"}) || !containsChoice(langs, Choice{"it", "Italiano"}) ||
		!containsChoice(langs, Choice{"en", "English"}) {
		t.Errorf("languages %v", langs)
	}
	if o := m.Orientations(); len(o) != 2 || o[1].Value != "on" || !strings.Contains(o[1].Label, "180") {
		t.Errorf("orientations %v", o)
	}
	f := m.Form()
	if f.Host != config.DefaultHost || f.Port != "6053" || !f.Doorbell || !f.SendApps || f.Key != "" || m.KeySaved() {
		t.Errorf("default form %+v", f)
	}
	if m.DefaultPCName() != "DESKTOP-1" {
		t.Errorf("default PC name %q", m.DefaultPCName())
	}
}

func containsChoice(cs []Choice, c Choice) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}

func TestSave(t *testing.T) {
	m, path, auto := newModel(t, nil)
	if err := m.Save(validForm()); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := cfg.Key()
	if cfg.Host != "luce.lan" || cfg.PCName != "STUDIO" || cfg.Language != "en" ||
		!cfg.SendAppNames || cfg.Notifications || !cfg.Doorbell || key != validKey {
		t.Errorf("saved %+v (key %q)", cfg, key)
	}
	if len(cfg.Ignore) != 2 || cfg.Ignore[1] != "nvidia broadcast.exe" || cfg.AppNames["rec.exe"] != "Recorder" {
		t.Errorf("lists %v %v", cfg.Ignore, cfg.AppNames)
	}
	if !auto.on {
		t.Error("autostart not enabled")
	}

	// The saved key is never shown again; an empty field keeps it, and
	// RemoveKey drops it.
	f := m.Form()
	if f.Key != "" || !m.KeySaved() || !strings.Contains(f.Names, "rec.exe = Recorder") {
		t.Errorf("form after save %+v", f)
	}
	if err := m.Save(f); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(path); cfg.KeyProtected == "" {
		t.Error("empty key field removed the saved key")
	}
	f.RemoveKey = true
	if err := m.Save(f); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(path); cfg.KeyProtected != "" || m.KeySaved() {
		t.Error("key not removed")
	}
}

func TestSaveValidation(t *testing.T) {
	tests := []struct {
		edit  func(*Form)
		field Field
		want  string
	}{
		{func(f *Form) { f.Host = " " }, FieldHost, "l'indirizzo è vuoto"},
		{func(f *Form) { f.Port = "70000" }, FieldPort, "la porta deve essere tra 1 e 65535"},
		{func(f *Form) { f.Port = "abc" }, FieldPort, "la porta"},
		{func(f *Form) { f.Key = "not-a-key" }, FieldKey, "la chiave deve essere quella in base64"},
		{func(f *Form) { f.Names = "ok.exe = Ok\nbroken line" }, FieldNames, "la riga 2 dei nomi"},
	}
	for _, tt := range tests {
		m, path, _ := newModel(t, nil)
		f := validForm()
		tt.edit(&f)
		err := m.Save(f)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != tt.field || !strings.Contains(fe.Message, tt.want) {
			t.Errorf("%q: error %v, want field %d with %q", tt.want, err, tt.field, tt.want)
			continue
		}
		if msg := m.SaveError(err); !strings.HasPrefix(msg, "Non salvato: ") {
			t.Errorf("save error %q", msg)
		}
		if c, _ := config.Load(path); c.Host != config.DefaultHost {
			t.Errorf("%q: invalid form was saved", tt.want)
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
		{espapi.ErrDevicePlaintext, false, "non usa la cifratura"},
		{context.DeadlineExceeded, false, "non è raggiungibile all'indirizzo luce.lan:6053"},
	}
	for _, tt := range tests {
		var got espapi.Options
		m, path, _ := newModel(t, func(_ context.Context, o espapi.Options) (TestResult, error) {
			got = o
			return TestResult{Device: "knockblock", Version: "2026.10.0"}, tt.err
		})
		ok, msg := m.Test(context.Background(), validForm())
		if ok != tt.ok || !strings.Contains(msg, tt.want) {
			t.Errorf("%v: %v %q, want %q", tt.err, ok, msg, tt.want)
		}
		if got.Address != "luce.lan:6053" || got.EncryptionKey != validKey {
			t.Errorf("tested with %+v", got)
		}
		if c, _ := config.Load(path); c.Host != config.DefaultHost {
			t.Error("the test saved the form")
		}
	}
	// An invalid form is not tested.
	m, _, _ := newModel(t, func(context.Context, espapi.Options) (TestResult, error) {
		t.Error("tested an invalid form")
		return TestResult{}, nil
	})
	f := validForm()
	f.Port = "0"
	if ok, msg := m.Test(context.Background(), f); ok || !strings.Contains(msg, "porta") {
		t.Errorf("invalid form: %v %q", ok, msg)
	}
}
