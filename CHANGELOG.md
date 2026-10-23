# Changelog

User-visible changes per release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). How to cut a release:
[`docs/releasing.md`](docs/releasing.md).

Every merge into `main` is released automatically as a new patch version;
the release page lists the merged PRs. This file collects the notable
changes per minor version.

## [Unreleased]

### Release

- Every merge into `main` publishes a GitHub release with the MSI and the
  firmware bundle, as the next patch version.

### Light (firmware)

- Doorbell: the board's BOOT button (or any GPIO, `doorbell_pin`) and the
  `Ring doorbell` button fire a `doorbell` event. The display shows a bell
  and the LED blinks white. Rings closer than 3 s count as one.
- `Flip display` switch turns the picture upside down; kept across reboots.

### PC app (Windows)

- The doorbell rings on every connected PC: a chime and a notification,
  also during calls. It can be turned off in the settings.
- Settings for the light's display orientation and for the doorbell.
- `knockctl ring` and `knockctl flip on|off` for trying them out.

## [0.1.0]

First release.

### Light (firmware)

- ESPHome firmware for the Waveshare ESP32-C6-LCD-1.47.
  - The status (`free`, `mic`, `camera`, `call`, `dnd`, `offline`) shows on
    the RGB LED and as a label on the display.
  - Display labels are in English and Italian, chosen with the
    `display_language` select. The choice is kept across reboots.
- Several PCs can drive the same light through the `report_presence`
  action, and the busiest status wins.
  - A PC that stops reporting expires after about 2 minutes, so the light
    never sticks on "call".
  - The display shows the app and PC causing the status. `privacy_mode`
    hides them.
- Works with Home Assistant alongside the PC app. Setting `status` there
  counts as a manual source, and `heartbeat` keeps it alive.
- Ships two ways:
  - an `esphome bundle` to flash from a folder;
  - a remote package for the ESPHome dashboard or the Home Assistant add-on.

### PC app (Windows)

- Tray app that detects microphone and webcam use from the Windows consent
  store and reports it to the light.
  - The report includes the app name (Teams, Discord, Zoom, browsers…),
    with custom names and an ignore list.
- Talks to the light over the ESPHome Native API with Noise encryption. If
  the connection drops, it reconnects in the background, including after
  sleep and network changes.
- Tray icon in the status colour, with a localized menu:
  - force Free or Do not disturb, or go back to Automatic;
  - open the settings;
  - Start with Windows;
  - Quit.
- Settings page in the browser:
  - opens automatically on first start;
  - served on `127.0.0.1` only while it is open;
  - includes a connection test;
  - stores the encryption key with Windows DPAPI.
- English and Italian, following the Windows display language. Adding a
  language needs only new locale files; see `docs/adding-a-language.md`.
- About 6.5 MB of memory when idle (private working set, measured by the
  release smoke test).
- Per-user MSI installer: no administrator rights, Start menu shortcut,
  starts with Windows (`AUTOSTART=0` turns this off).
