# Presence: several PCs, apps and privacy

The light shows **one** status for the whole room, but several PCs may be on
at once (a desktop and a laptop, for example). Each PC reports its own
status, along with the app causing it. The device combines the reports and
shows the strongest one, with the app and PC behind it. This is similar to
Discord's "Rich Presence": "In a call · Teams · PC-STUDIO".

## Sources and the combined status

The firmware component `firmware/components/knockblock_presence` keeps a
table of up to 8 **sources**:

| Source | Reported by | Kept alive by |
|--------|-------------|---------------|
| A PC (identified by its name) | the tray app, via the API action `report_presence(pc, status, app)` | the next `report_presence` |
| `manual` (no name) | anyone setting the `status` select directly: Home Assistant, `knockctl status` | the `heartbeat` button |

- A source that sends nothing for `watchdog_timeout` (120 s on the board) is
  dropped. A PC that shuts down or loses the network therefore cannot leave
  the light on "call" (CLAUDE.md §4).
- Reporting `offline` drops the source at once, for example on a clean
  shutdown. An unknown status is ignored and logged.
- **The strongest live status wins:** `call` > `camera` > `mic` > `dnd` >
  `muted` > `free`. On a tie, the most recent report wins. With no live source the
  status is `offline`.
- The `status` select always shows the combined status. `active_app` and
  `active_pc` show the app and PC of the winning source; both are empty for
  the manual source and for `offline`.
- PC and app names are cut to 32 bytes, at a UTF-8 character boundary. When
  the table is full, the least recently seen source is replaced.

## Entities and actions (Native API, also seen by Home Assistant)

| Entity | Type | Notes |
|--------|------|-------|
| `status` | select | combined status; setting it reports the manual source |
| `display_language` | select | unchanged |
| `heartbeat` | button | keeps the manual source alive |
| `status_label` | text sensor | the localized label shown on the display |
| `active_app` | text sensor | e.g. `Teams` |
| `active_pc` | text sensor | e.g. `PC-STUDIO` |
| `privacy_mode` | switch (board only) | hides app and PC **on the display**; Home Assistant still sees them |
| `report_presence` | API action | arguments `pc`, `status`, `app` (all strings) |

## Display

The localized status is drawn large, and under it the line `<app> · <pc>` in a
smaller font. If that line is too wide, only the app is shown. The line is
left out in privacy mode, and when the status has no app or PC. Display
labels are checked against the font at `esphome config` time. App and PC
names come from the PCs at run time, so characters outside the detail font's
glyphs are not drawn.

## What the tray app sends (milestones 5–6)

- On every change, and otherwise every ~30 s (well within the timeout), it
  sends `report_presence(pc, status, app)`:
  - `pc` is the Windows computer name, unless overridden in the settings;
  - `status` comes from the status logic of CLAUDE.md §5.3; a manual override
    from the tray menu is simply the status it reports;
  - `app` is the friendly name of the app using the camera or microphone,
    taken from the registry entry that detection already reads (§5.2).
- Friendly names come from a built-in table that the settings can extend or
  override. Unknown apps show their executable name without `.exe`.

  | Detected | Shown as |
  |----------|----------|
  | `ms-teams.exe`, `Teams.exe`, package `MSTeams_8wekyb3d8bbwe` | Teams |
  | `Discord.exe` | Discord |
  | `Zoom.exe` | Zoom |
  | `slack.exe` | Slack |
  | `obs64.exe` | OBS |
  | `chrome.exe`, `msedge.exe`, `firefox.exe` | Chrome, Edge, Firefox |

  Browsers can only be reported as the browser: Meet or Teams on the web
  show as "Chrome" or "Edge".
- When the camera and the microphone are used by different apps, the app
  using the camera is reported, since it decides `camera` / `call`.
- A setting **"Do not send app names"** sends an empty `app`, for people who
  do not want app names to reach the device or Home Assistant at all.
- On exit the app sends `offline` for its PC.
