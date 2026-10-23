## Downloads

### `KnockBlock-<version>.msi`: Windows tray app

Double-click to install. The setup, in English or Italian like Windows, asks:

- **for everyone** (in `Program Files`, asks for administrator rights; the
  default) or **only for me** (in `%LOCALAPPDATA%\Programs`, no rights
  needed);
- the folder, which you can change;
- confirmation; at the end, "Start KnockBlock" (checked).

KnockBlock gets a Start menu shortcut and starts with Windows. Installing a
newer version over an older one (installed the same way) closes the running
app, tells you it is updating, and starts it again.

Silent installs: `msiexec /i KnockBlock-<version>.msi /qn` (for everyone, as
administrator); add `ALLUSERS="" MSIINSTALLPERUSER=1` for the current user,
`APPLICATIONFOLDER="D:\Apps\KnockBlock"` for another folder, `AUTOSTART=0` to
not start with Windows.

If you installed 0.1.x (always "only for me") and now choose "for everyone",
uninstall the old one from Settings → Apps.

On first start the app opens its settings window: enter the light's address
(default `knockblock.local`) and the API encryption key, press **Test
connection**, then **Save**. You can reopen the window from the tray menu
("Settings…"). The tray icon is grey while the light is unreachable.

Uninstall from Settings → Apps.

### `knockctl-<version>.exe`: command-line tool

For trying the light from a terminal, without the tray app: `knockctl.exe
-addr knockblock.local -key <api key> list`, `… ring`, `… agent`. See the
README.

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
