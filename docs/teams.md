# Microsoft Teams integration

Like Discord, Teams keeps the microphone open while you are muted, so
Windows cannot tell a muted meeting from a live one. The **Integrations**
tab can follow the Teams mute button instead: muted with no webcam, the
light shows `muted` (blue).

It uses the **third-party app API** of the new Teams (the one Stream Deck
and Elgato use): a WebSocket that Teams opens on `127.0.0.1:8124`, reachable
only from the same PC. No developer account or application is needed.

1. In Teams, turn the API on once: **Settings → Privacy → Manage API →
   Enable API**.
2. In KnockBlock's settings, **Integrations** tab, tick **Microsoft Teams**.
3. In your next meeting Teams asks whether to allow KnockBlock. Allow it:
   Teams gives KnockBlock a token, kept in `%APPDATA%\KnockBlock\teams-token`
   (encrypted for the Windows user), and it connects by itself from then
   on.

The mute state counts only during a meeting; outside meetings the
microphone is judged as for any other app.

Protocol notes (protocol-version 2.0.0) come from community clients, for
example [Teams.ThirdPartyAppApi](https://github.com/ferenyl/Teams.ThirdPartyAppApi):
Teams sends `meetingUpdate` (with `meetingState.isMuted`, `isInMeeting`,
`meetingPermissions.canPair`) and `tokenRefresh`; while not paired and
`canPair` is true, KnockBlock sends the harmless `query-state` command,
which makes Teams show its consent prompt.
TODO(verify): that `query-state` is enough to start the pairing prompt.
