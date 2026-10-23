# Detection notes (Windows)

CLAUDE.md §5.2 asks to verify on a real machine how the consent store
reports microphone and camera use. Fill this in from `knockctl detect` runs.

## How to check

1. Download `knockctl.exe` from the CI artifacts (the "Go" job of any PR).
2. In a terminal, with nothing using the mic or camera, run:
   ```
   knockctl.exe detect
   ```
3. Start a call (Teams, Discord, …), run it again, and compare: the app in the
   call should show `YES` in the IN USE column.
4. End the call and run it a third time: STOP should now have a time.

The rule KnockBlock uses: **in use = START set and STOP empty**.

## Findings

| App | Version | Device | Key (desktop path or Store package) | Reported correctly? | Notes |
|-----|---------|--------|------------------------------------|---------------------|-------|
| Teams (new) | | mic / camera | | | |
| Discord | | mic | | | |
| Zoom | | | | | |
| Browser call (Meet) | | | | | |

## Known caveats

- Browsers show as the browser ("Chrome", "Edge"), not the site.
- Some apps keep the microphone open outside calls (voice chat, NVIDIA
  Broadcast, virtual audio devices). Put them in the ignore list
  (`knockctl agent -ignore app.exe`, later in the settings page).
- The consent store is per user (HKCU): apps running as another user are
  not seen.
