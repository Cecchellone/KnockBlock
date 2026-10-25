# Discord integration

Discord keeps the microphone open while you are muted in a call, so
Windows (and KnockBlock's detection) cannot tell a muted call from a live
one. The **Integrations** tab of the settings can follow Discord's own mute
and deafen buttons instead: muted or deafened with no webcam, the light
shows `muted` (blue).

It works like Logitech G HUB or a Stream Deck: KnockBlock talks to the
Discord app running on the same PC through Discord's local RPC interface (a
named pipe). The first time, Discord shows its own window asking to allow
KnockBlock; after that it connects by itself whenever Discord runs. The
token Discord grants is kept in `%APPDATA%\KnockBlock\discord-token`,
encrypted for the Windows user (DPAPI), and refreshed when it expires.

Reading the files Discord keeps on disk was tried first and does not work:
Discord does not write the in-call mute there when the button is pressed.

## The Discord application (one time, by the owner)

Reading voice settings over RPC needs an OAuth2 application registered on
Discord's developer portal. Its client ID and secret are built into release
builds from the repository's secrets, so users never type any code.

1. Open <https://discord.com/developers/applications> and create an
   application named **KnockBlock** (an icon helps: it shows in Discord's
   consent window).
2. In **OAuth2**, copy the **Client ID**, then **Reset Secret** and copy
   the **Client Secret**.
3. Still in **OAuth2**, under **Redirects**, add `http://localhost` and save
   (the token exchange names it; nothing ever opens it).
4. On GitHub, in the repository's **Settings → Secrets and variables →
   Actions**, add two repository secrets: `DISCORD_CLIENT_ID` and
   `DISCORD_CLIENT_SECRET`.

The next MSI built by the release workflow includes the application. In a
build without it, the Discord option is greyed out and says it is not
available. For a local build, add `"discord_client_id"` and
`"discord_client_secret"` to `config.json` by hand.

## Who can use it

The RPC voice scopes (`rpc`, `rpc.voice.read`) are restricted by Discord:
until Discord approves the application, only its owner and the people added
as **App Testers** in the developer portal (at most 50) can allow it. For
anyone else Discord refuses the consent, and the settings say it was not
allowed. Discord no longer approves new RPC applications in practice, so
this is an option for the owner and a few friends, not for everyone.

The secret is inside the installed program: anyone could extract it. With
the restricted scopes that only lets them ask the same consent of Discord
users, which works only for the testers above.

## States shown in the settings

| State | Meaning |
|-------|---------|
| Off | the option is off |
| Not available | this build has no Discord application |
| Waiting for Discord | Discord is not running; KnockBlock retries every 10 s |
| Allow KnockBlock in Discord | Discord is showing its consent window |
| Not allowed | the consent was declined (or the user is not a tester); turn the option off and on to ask again |
| Connected | the light follows the mute and deafen buttons |
