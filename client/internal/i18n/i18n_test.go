package i18n

import (
	"slices"
	"testing"
	"testing/fstest"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(fstest.MapFS{
		"en.json":    {Data: []byte(`{"hello": "Hello {name}", "bye": "Bye", "only_en": "English"}`)},
		"it.json":    {Data: []byte(`{"hello": "Ciao {name}", "bye": "Ciao"}`)},
		"pt-BR.json": {Data: []byte(`{"hello": "Olá {name}"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMatch(t *testing.T) {
	c := testCatalog(t)
	tests := []struct {
		prefs []string
		want  string
	}{
		{[]string{"it-IT"}, "it"},
		{[]string{"it"}, "it"},
		{[]string{"IT_it"}, "it"},
		{[]string{"pt-BR"}, "pt-BR"},
		{[]string{"pt-PT"}, "en"}, // no "pt" file; only pt-BR
		{[]string{"de-DE", "it-IT"}, "it"},
		{[]string{"de-DE"}, "en"},
		{[]string{"", "  "}, "en"},
		{nil, "en"},
	}
	for _, tt := range tests {
		if got := c.Match(tt.prefs...); got != tt.want {
			t.Errorf("Match(%q) = %q, want %q", tt.prefs, got, tt.want)
		}
	}
	if got := c.Languages(); !slices.Equal(got, []string{"en", "it", "pt-BR"}) {
		t.Errorf("Languages = %v", got)
	}
}

func TestTranslator(t *testing.T) {
	it := testCatalog(t).Translator("it")
	tests := []struct {
		key  string
		args []string
		want string
	}{
		{"hello", []string{"name", "Ada"}, "Ciao Ada"},
		{"only_en", nil, "English"},   // falls back to English
		{"missing", nil, "missing"},   // falls back to the key
		{"hello", nil, "Ciao {name}"}, // unreplaced placeholder stays visible
	}
	for _, tt := range tests {
		if got := it.T(tt.key, tt.args...); got != tt.want {
			t.Errorf("T(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLoadNeedsEnglish(t *testing.T) {
	if _, err := Load(fstest.MapFS{"it.json": {Data: []byte(`{}`)}}); err == nil {
		t.Error("catalog without en.json accepted")
	}
}

func TestPlaceholders(t *testing.T) {
	if got := Placeholders("{b} and {a} and {b}"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Placeholders = %v", got)
	}
}
