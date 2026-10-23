// Package i18n loads the embedded locale files, picks the user language
// (setting, then Windows UI language, then English) and substitutes {name}
// placeholders. English is the fallback for every missing key (CLAUDE.md §6.1).
package i18n
