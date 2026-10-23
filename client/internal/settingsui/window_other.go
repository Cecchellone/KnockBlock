//go:build !windows

package settingsui

import "errors"

// Window is the settings window. Only Windows has a view so far; another
// system adds its own over the same Model.
type Window struct{ deps Deps }

// NewWindow returns a closed settings window.
func NewWindow(d Deps) *Window {
	d.defaults()
	return &Window{deps: d}
}

// Open reports that this system has no settings window yet.
func (w *Window) Open() error { return errors.ErrUnsupported }

// Close does nothing.
func (w *Window) Close() {}
