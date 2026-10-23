## Downloads

### `KnockBlock-<version>.msi`: Windows tray app

Double-click to install. No administrator rights are needed: the app is
installed for the current user in `%LOCALAPPDATA%\Programs\KnockBlock`, with a
Start menu shortcut, and starts with Windows. To install without starting
with Windows:

```
msiexec /i KnockBlock-<version>.msi AUTOSTART=0
```

Uninstall from Settings → Apps.

### `knockblock-<version>.esphomebundle.tar.gz`: device firmware

The ESPHome configuration for the Waveshare ESP32-C6-LCD-1.47, with
everything it needs. To flash the board (ESPHome at the version in
`proto/VERSION`):

1. Extract the archive into a folder.
2. Edit `secrets.yaml` in that folder: set your Wi-Fi name and password, and
   an API encryption key generated with `openssl rand -base64 32`. Enter the
   same key in the tray app and in Home Assistant.
3. In that folder, run `esphome run knockblock.yaml`.

If you use the ESPHome dashboard or the Home Assistant add-on, you can use
the remote package instead; see `firmware/examples/remote-package.yaml`.
