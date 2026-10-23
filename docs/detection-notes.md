# Detection notes (Windows)

CLAUDE.md §5.2 asks to verify on a real machine how the consent store
reports microphone and camera use. Fill this in from `knockctl detect` runs.

The **microphone** comes from the Windows audio sessions
(WASAPI): an app uses it when it has an *active capture session* on a
recording device. The consent store is still used for the **camera**, and
for the microphone if the audio sessions cannot be read.

## How to check

1. Download `knockctl-<version>.exe` from the latest release.
2. In PowerShell, in the download folder, run:
   ```
   .\knockctl.exe detect
   ```
   The first table is the consent store; the second, **AUDIO SESSIONS**,
   lists the programs with a recording session and `YES` for those
   recording right now.
3. Start a call (Teams, Discord, …), run it again, and compare.

The consent store rule: **in use = START set and STOP empty**. The audio
session rule: **in use = a capture session in the Active state**.

## Findings

### Discord (1.0.9261, Windows 11, October 2026)

- The consent store kept an entry of an **old** Discord version
  (`…\Discord\app-1.0.9254\Discord.exe`) with STOP empty for two months:
  Discord updates itself into a new folder, and that version never wrote
  its STOP time. KnockBlock compared running programs by executable name
  only, so the new `Discord.exe` kept the stale entry alive: the light
  showed "mic" whenever Discord was open, in a channel or not.
- The **current** version's entry was not marked in use even during a call
  (Krisp and automatic input sensitivity on): for Discord the consent store
  does not say when the microphone is really used.
- Fixes: stale entries are matched to running programs by full path, and
  the microphone comes from the audio sessions.

| App | Version | Device | Key (desktop path or Store package) | Reported correctly? | Notes |
|-----|---------|--------|------------------------------------|---------------------|-------|
| Discord | 1.0.9261 | mic | `…#Discord#app-1.0.9261#Discord.exe` | No (consent store) | see above |
| Teams (new) | | mic / camera | | | |
| Zoom | | | | | |
| Browser call (Meet) | | | | | |

## Known caveats

- Browsers show as the browser ("Chrome", "Edge") unless a window title
  names a known web app ("Teams (Firefox)").
- Programs that record the microphone for other apps (NVIDIA Broadcast,
  Voicemeeter, Krisp) are ignored by default; the app using their virtual
  microphone is the one reported. Other apps that keep the microphone open
  outside calls go in the ignore list (settings, Apps tab).
- An app muted inside a call may keep its capture session active, so it
  still shows as "mic".
- The consent store is per user (HKCU): apps running as another user are
  not seen.
