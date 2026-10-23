package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Fallback is the language used for missing keys and unknown languages.
const Fallback = "en"

// Catalog holds every language found in a set of <lang>.json files.
type Catalog struct {
	langs map[string]map[string]string // language code → key → text
}

// Load reads every *.json file in fsys; the file name is the language code.
// The fallback language must be present.
func Load(fsys fs.FS) (*Catalog, error) {
	files, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	c := &Catalog{langs: make(map[string]map[string]string, len(files))}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var msgs map[string]string
		if err := json.Unmarshal(data, &msgs); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", f, err)
		}
		c.langs[strings.TrimSuffix(path.Base(f), ".json")] = msgs
	}
	if c.langs[Fallback] == nil {
		return nil, fmt.Errorf("i18n: no %s.json", Fallback)
	}
	return c, nil
}

// Languages returns the available language codes, sorted.
func (c *Catalog) Languages() []string {
	out := make([]string, 0, len(c.langs))
	for l := range c.langs {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

// Messages returns the raw messages of a language (nil if unknown).
func (c *Catalog) Messages(lang string) map[string]string { return c.langs[lang] }

// Match returns the best available language for the preferences, in order:
// an exact match ("pt-BR"), else the base language ("it-IT" → "it"); the
// fallback language when nothing matches. Matching ignores case and accepts
// '_' as separator.
func (c *Catalog) Match(prefs ...string) string {
	for _, p := range prefs {
		p = strings.ReplaceAll(strings.TrimSpace(p), "_", "-")
		if p == "" {
			continue
		}
		for l := range c.langs {
			if strings.EqualFold(l, p) {
				return l
			}
		}
		base, _, _ := strings.Cut(p, "-")
		for l := range c.langs {
			if strings.EqualFold(l, base) {
				return l
			}
		}
	}
	return Fallback
}

// Translator returns texts in one language, falling back to English.
type Translator struct {
	Lang     string
	msgs     map[string]string
	fallback map[string]string
}

// Translator returns a Translator for lang (see Match for choosing it).
func (c *Catalog) Translator(lang string) *Translator {
	return &Translator{Lang: lang, msgs: c.langs[lang], fallback: c.langs[Fallback]}
}

// T returns the text for key with {name} placeholders replaced; args are
// name, value pairs. A key missing in the language falls back to English,
// and a key missing everywhere is returned as is.
func (t *Translator) T(key string, args ...string) string {
	s, ok := t.msgs[key]
	if !ok {
		if s, ok = t.fallback[key]; !ok {
			return key
		}
	}
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", args[i+1])
	}
	return s
}

var placeholderRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// Placeholders returns the sorted, distinct {name} placeholders of s.
func Placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return slices.Compact(out)
}
