# KnockBlock

*Like an ad blocker, but for people knocking on your door.*

KnockBlock is a small status light that hangs outside your room and tells everyone in the house whether you are free, have the microphone on, have the webcam on, or are in a call. Your PC updates it automatically, so you never have to remember to flip a sign again.

## Getting started

1. **Flash the light.** Download `knockblock-<version>.esphomebundle.tar.gz` from the [latest release](https://github.com/Cecchellone/KnockBlock/releases/latest), extract it, put your Wi-Fi and a new API key (`openssl rand -base64 32`) in `secrets.yaml`, and run `esphome run knockblock.yaml` with the board on USB. Using the ESPHome dashboard or Home Assistant instead? See [the remote package](#installing-the-firmware-from-the-esphome-dashboard--home-assistant).
2. **Install the PC app.** Run `KnockBlock-<version>.msi`: choose *for everyone* (Program Files, asks for administrator rights) or *only for me* (no rights needed), confirm the folder, install. Leave "Start KnockBlock" checked at the end: on first start it opens its settings window.
3. **Connect them.** Paste the same API key, check the device address (default `knockblock.local`), press **Test connection**, then **Save**. The tray icon turns green and the light shows "Free".

From then on, the light follows your microphone and webcam. Several PCs can drive the same light; the busiest status wins. See [`docs/presence.md`](docs/presence.md).

## How it works

- **The light** is a [Waveshare ESP32-C6-LCD-1.47](https://www.waveshare.com/) board running [ESPHome](https://esphome.io/). It shows the status with a coloured LED and a label on its small display.
- **The PC app** is a tiny Windows tray program that notices when an app is using your microphone or webcam and tells the light, using the ESPHome Native API (encrypted, same protocol Home Assistant uses).
- **Home Assistant** is optional. If you have it, it discovers the light automatically and can use it alongside the PC app.

| Status   | Meaning                          |
|----------|----------------------------------|
| `free`   | Come in                          |
| `away`   | Not at the PC (locked or idle); the screen rests: black with a small dot |
| `muted`  | In a call, microphone muted and webcam off |
| `mic`    | Microphone is on                 |
| `camera` | Webcam is on                     |
| `call`   | In a call — please don't knock   |
| `dnd`    | Do not disturb (manual)          |
| `night`  | Sleeping (Night mode switch, e.g. from Home Assistant) |
| `offline`| The PC is off or unreachable     |

## Goals

- **Featherweight:** the tray app aims to stay under 15 MB of RAM. No GUI framework sitting in memory.
- **Native connection:** talks to the device directly over the ESPHome Native API. No REST, no MQTT, no cloud.
- **Multilingual:** English and Italian out of the box, on the PC and on the device display. Adding a language means adding a file.

## Planned

- Infrared transmitter to control an old air conditioner.
- DHT22 temperature and humidity sensor.

## Repository layout

| Path | What it is |
|------|------------|
| `firmware/` | ESPHome configuration for the device (and a `host` build for tests) |
| `client/` | Go module with the Windows tray app |
| `proto/` | ESPHome Native API protobuf files, pinned to the version in `proto/VERSION` |
| `installer/` | MSI definition and its texts (WiX v5, built on Windows) |
| `scripts/` | Checks, integration tests, release builds |
| `docs/` | Protocol notes and contributor guides; [`releasing.md`](docs/releasing.md) for maintainers |

## Development

The Go module builds and tests on Linux; Windows-only code sits behind build tags.

```sh
scripts/check-go.sh   # gofmt, go vet, staticcheck, tests, Windows cross-build (dist/knockblock.exe)
```

The required Go toolchain is pinned in `client/go.mod` and downloaded automatically by the `go` command.

### Downloads

Every merge into `main` publishes a new [release](https://github.com/Cecchellone/KnockBlock/releases) (the next patch version). Each one has:

- `KnockBlock-<version>.msi`: the Windows tray app. The setup (English or Italian, as Windows) installs for everyone or only for you, in a folder you can change. Updating closes a running KnockBlock, tells you, and starts it again.
- `knockblock-<version>.esphomebundle.tar.gz`: the firmware configuration. Extract it, fill in `secrets.yaml`, then run `esphome run knockblock.yaml`.

Details are in [`docs/release-notes.md`](docs/release-notes.md). Build them locally with `scripts/build-msi.ps1` (on Windows; needs the WiX v5 .NET tool, see the script) and `scripts/build-firmware-bundle.sh` (needs ESPHome).

### The tray app

After installing the MSI, KnockBlock starts with Windows and sits in the notification area:

- The **icon colour** follows this PC's status: green free, amber microphone, orange camera, red call, purple do not disturb. It is grey while the light is unreachable.
- The **menu** shows the status and the connection. You can force "Free" or "Do not disturb" ("Automatic" goes back to detection), open the settings, toggle "Start with Windows", or quit.
- **Settings…** (or a double click on the icon) opens the settings window, in four tabs: **Connection** (address, port, encryption key, Test connection), **Device** (the light's own settings, read from it and saved on it, the same ones Home Assistant shows: display language, orientation, display brightness and LED brightness as sliders that change the light while you drag them, privacy), **This PC** (PC name, language, app names, doorbell, notifications: none, text only, sound only, or text and sound, Start with Windows) and **Apps** (ignored apps, custom names). Ctrl+Tab switches tabs. Saving does not drop the connection unless the address or the key changed. Everything is in your language (English or Italian; English is the fallback). The window takes no memory when closed.
- The settings are stored in `%APPDATA%\KnockBlock\config.json`; the encryption key is encrypted with Windows DPAPI and never shown again once saved. The app reloads the file as soon as it changes. If you edit the file by hand, you can put the key in plain text as `"key": "…"`: the app encrypts it on the next start and removes the plain copy.
- **Doorbell**: when someone presses the light's button, every PC running KnockBlock shows a Windows notification, "Someone is at your door", also during calls (turn it off in the settings). The light shows a bell for a few seconds.
- **Logs** go to `%LOCALAPPDATA%\KnockBlock\logs\knockblock.log`. Run `knockblock.exe -debug -debug-mem 30s` to log details and memory use.

### Trying the PC side without the tray app

`knockctl` (a command-line tool, attached to every release as `knockctl-<version>.exe`) runs the same presence loop as the tray app:

```sh
knockctl.exe -addr knockblock.local -key <api key> agent     # detect mic/camera use and report it until Ctrl-C
knockctl.exe detect                                            # show what Windows reports about mic/camera use
knockctl.exe -addr knockblock.local -key <api key> ring       # ring the doorbell, as if the button were pressed
knockctl.exe -addr knockblock.local -key <api key> flip on    # turn the light's display upside down (off: back)
```

Options: `-pc NAME` (default: the computer name), `-ignore app.exe,…`, `-no-app-names`, `-debug-mem 30s`.

### Display, doorbell and orientation

- The display shows the status in large letters and, in a darker bar at the top, the PC (left), the app (middle) and the time (right), or how many minutes the call has lasted. **Privacy mode** hides the PC and the app. The time comes from the internet (SNTP): if you are not in the `Europe/Rome` time zone, set `timezone` in your device config's `substitutions:` (e.g. `timezone: Europe/London`).
- **LED brightness** (Home Assistant, or the PC settings' Device tab) sets how bright the status LED is.

- The doorbell is the board's **BOOT** button. To use an external push button instead, wire it between a free GPIO and GND and set `doorbell_pin` in your device config's `substitutions:` (e.g. `doorbell_pin: GPIO4`). Home Assistant sees a `Doorbell` event (device class `doorbell`) and a `Ring doorbell` button.
- If the light hangs the other way round, turn on the **Flip display** switch (Home Assistant), or choose "Upside down" in the PC app's settings. The device remembers it.

### Installing the firmware from the ESPHome dashboard / Home Assistant

No checkout needed: KnockBlock is available as a remote ESPHome package. Create a device in the ESPHome dashboard (or the Home Assistant ESPHome add-on) and use [`firmware/examples/remote-package.yaml`](firmware/examples/remote-package.yaml) as its configuration. It pulls `firmware/knockblock-package.yaml` and the `knockblock_i18n` external component from this repository at `knockblock_ref` (default `main`). Your config only adds Wi-Fi and the API encryption key.

### Building the firmware from a checkout

Firmware (needs ESPHome, at the version in `proto/VERSION`):

```sh
pip install "esphome==$(cat proto/VERSION)"
cd firmware
cp secrets.example.yaml secrets.yaml      # then fill in Wi-Fi and the API key
esphome run knockblock.yaml               # compile and flash the board
esphome compile host-test.yaml            # same logic as a Linux process, for tests
```

## License

KnockBlock is free software, released under the [GNU General Public License v3.0](LICENSE).
