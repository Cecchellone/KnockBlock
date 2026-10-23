package locales

import (
	"maps"
	"slices"
	"testing"

	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
)

// Every locale must have exactly the keys of en.json, with the same
// placeholders (CLAUDE.md §6.1).
func TestLocalesMatchEnglish(t *testing.T) {
	c, err := i18n.Load(FS)
	if err != nil {
		t.Fatal(err)
	}
	en := c.Messages(i18n.Fallback)
	if len(c.Languages()) < 2 {
		t.Fatalf("languages %v; English and Italian ship from day one", c.Languages())
	}
	for _, lang := range c.Languages() {
		msgs := c.Messages(lang)
		for key, text := range en {
			got, ok := msgs[key]
			if !ok {
				t.Errorf("%s.json: missing key %q", lang, key)
				continue
			}
			if !slices.Equal(i18n.Placeholders(got), i18n.Placeholders(text)) {
				t.Errorf("%s.json: %q has placeholders %v, en.json has %v", lang, key, i18n.Placeholders(got), i18n.Placeholders(text))
			}
		}
		for _, key := range slices.Sorted(maps.Keys(msgs)) {
			if _, ok := en[key]; !ok {
				t.Errorf("%s.json: key %q is not in en.json", lang, key)
			}
		}
	}
}
