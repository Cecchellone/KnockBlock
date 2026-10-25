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

- During a call the duration in the info bar is bold, in a red pill (white
  on a red screen), so it is not mistaken for the clock.
- Resting screen: with no PC connected, or with every PC away, the display
  is black with only a small dot, so it does not light up the hallway.
- New status `away` (a PC that is locked or idle), the weakest after
  `offline`.

- Doorbell: the board's BOOT button (or any GPIO, `doorbell_pin`) and the
  `Ring doorbell` button fire a `doorbell` event. The display shows a bell
  and the LED blinks white. Rings closer than 3 s count as one.
- `Flip display` switch turns the picture upside down; kept across reboots.
- New layout: a darker info bar at the top shows the PC on the left, the app
  in the middle and the time on the right, or the minutes since the call
  started during a call. The time comes from the internet (SNTP); set
  `timezone` if you are not in Italy's time zone.
- `LED brightness` setting (5–100 %, kept across reboots) for the status LED.
- New status `muted` (blue): in a call with the microphone muted and the
  webcam off. It ranks below `dnd`.
- `Night mode` switch: the light says "Sleeping" on a dark blue, dimmed,
  until turned off; status `night`, above `dnd`, below a PC in a call.
- `Phone call` switch for Home Assistant automations (e.g. the Android
  companion app's phone state): "In a call" with the phone icon and the
  duration; it turns itself off after 4 hours. Examples in
  docs/home-assistant.md.
- During a call the info bar shows how long it has lasted (minutes and
  seconds) instead of the time.
- Messages from a PC (`show_message`) show on the display with a white bar
  at the bottom that shrinks towards the centre, for 4 s plus 0.2 s per
  character, between 5 and 30 s.
- Home Assistant actions `set_status` (any status with your own label,
  colour, source, app, icons and duration) and `display_message` (a
  message with source, app, colour and duration). Examples in
  docs/home-assistant.md.
- Icons at the bottom of the display show the phone (call open), the
  microphone (on, muted or unused) and the webcam (on or off). PC apps
  report them with the new `report_activity` action; `active_devices`
  shows them to Home Assistant.

### PC app (Windows)

- The doorbell notification has quick replies: "Come in", "5 minutes",
  "Not now".
- Microsoft Teams' mute button can drive the light, like Discord's: tick
  it in the Integrations tab after turning on Teams' third-party API
  (docs/teams.md).
- "Away": the PC reports `away` while it is locked or unused for 10
  minutes (setting in This PC, on by default).

- The doorbell notification has a reply box when it shows on screen
  (notification mode "text" or "text and sound"): type an answer, press
  Enter, and it appears on the light (5 to 30 s, by its length).
- Discord's mute and deafen buttons can drive the light: tick Discord in
  the new Integrations tab and allow KnockBlock in the window Discord
  shows (docs/discord.md). Discord keeps the microphone open while muted,
  so Windows alone cannot tell.
- The settings tabs are now This PC, KnockBlock, Integrations, Apps,
  Connection; the window opens on Connection only until a key is saved.
- A connection attempt to the light can no longer hang: after an outage
  the app reconnects within a minute or two.
- The settings window has no Save or Cancel: every change is saved at
  once (a text field when you leave it), on the light too.
- The webcam is detected again. Recent Windows 11 builds stopped recording
  camera use where the app looked; the app now asks Windows' camera service
  which programs are streaming. A webcam used through NVIDIA Broadcast's
  virtual camera counts too, shown with the app in the call.
- A call with the microphone muted in Windows (the sound settings, a mute
  key, or the app muting its own recording) and the webcam off shows as
  `muted`. `knockctl detect live` prints what detection sees every second.

- The doorbell rings on every connected PC: a standard Windows notification,
  also during calls. It can be turned off in the settings.
- Settings for the light's display orientation and for the doorbell.
- `knockctl ring` and `knockctl flip on|off` for trying them out.
- The settings are a native Windows window instead of a page in the
  browser. It exists only while open.
- New setup: choose for everyone (Program Files) or only for you, the
  folder, then confirm; "Start KnockBlock" at the end. English and Italian.
- Updating closes the running app (it says it is updating and tells the
  light it goes offline) and starts it again; the new version says it was
  updated.
- Notifications are plain Windows notifications. A setting chooses how they
  show: none, text only (default), sound only (Windows' notification
  sound), or text and sound. Notifications, the doorbell's included, did
  not show at all before: the data passed to Windows was laid out wrong.
- Double-clicking the tray icon opens the settings.
- The display and LED brightness are sliders; the light follows them while
  you drag, and goes back if you close the window without saving.
- The microphone is detected from the Windows audio sessions: an app counts
  only while it is really recording. Discord no longer shows as using the
  microphone when it is just open, and an entry left by an old version of
  an app no longer counts. `knockctl detect` lists the audio sessions.
- The settings window has tabs: Connection, Device, This PC, Apps. The
  Device tab reads the light's own settings (display language, orientation,
  display brightness, LED brightness, privacy) and saves them on the light.
- Saving the settings no longer drops the connection unless the address or
  the key changed.
- An app that crashed or was closed no longer shows as using the
  microphone (stale Windows entries are ignored).

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
