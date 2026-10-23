//go:build windows

package i18n

import "golang.org/x/sys/windows"

// SystemLanguages returns the user's Windows UI languages, most preferred
// first, as BCP 47 codes ("it-IT").
func SystemLanguages() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return langs
}
