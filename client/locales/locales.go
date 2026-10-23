// Package locales embeds the tray app's translations, one JSON file per
// language (BCP 47 code as the file name). en.json is the source of truth.
// Adding a language means adding a file here; see docs/adding-a-language.md.
package locales

import "embed"

// FS holds every <lang>.json file.
//
//go:embed *.json
var FS embed.FS
