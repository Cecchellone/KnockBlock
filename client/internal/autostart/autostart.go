// Package autostart turns "Start with Windows" on and off: the value
// KnockBlock under HKCU\Software\Microsoft\Windows\CurrentVersion\Run
// (CLAUDE.md §5.5). The MSI writes the same value, so uninstalling removes
// it.
package autostart

// ValueName is the Run value used by the app and the installer.
const ValueName = "KnockBlock"
