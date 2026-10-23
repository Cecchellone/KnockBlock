//go:build !windows

package i18n

import (
	"os"
	"strings"
)

// SystemLanguages returns the language from LC_ALL, LC_MESSAGES or LANG
// ("it_IT.UTF-8" → "it-IT"), for development on non-Windows systems.
func SystemLanguages() []string {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if l := os.Getenv(v); l != "" && l != "C" && l != "POSIX" {
			l, _, _ = strings.Cut(l, ".")
			return []string{strings.ReplaceAll(l, "_", "-")}
		}
	}
	return nil
}
