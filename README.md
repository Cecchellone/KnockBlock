# KnockBlock

*Like an ad blocker, but for people knocking on your door.*

KnockBlock is a small status light that hangs outside your room and tells everyone in the house whether you are free, have the microphone on, have the webcam on, or are in a call. Your PC updates it automatically, so you never have to remember to flip a sign again.

## Getting started

1. **Flash the light.** Download `knockblock-<version>.esphomebundle.tar.gz` from the [latest release](https://github.com/Cecchellone/KnockBlock/releases/latest), extract it, put your Wi-Fi and a new API key (`openssl rand -base64 32`) in `secrets.yaml`, and run `esphome run knockblock.yaml` with the board on USB. Using the ESPHome dashboard or Home Assistant instead? See [the remote package](#installing-the-firmware-from-the-esphome-dashboard--home-assistant).
2. **Install the PC app.** Run `KnockBlock-<version>.msi` (no administrator rights needed). On first start it opens its settings page in your browser.
3. **Connect them.** Paste the same API key, check the device address (default `knockblock.local`), press **Test connection**, then **Save**. The tray icon turns green and the light shows "Free".

From then on, the light follows your microphone and webcam. Several PCs can drive the same light; the busiest status wins. See [`docs/presence.md`](docs/presence.md).

## How it works

- **The light** is a [Waveshare ESP32-C6-LCD-1.47](https://www.waveshare.com/) board running [ESPHome](https://esphome.io/). It shows the status with a coloured LED and a label on its small display.
- **The PC app** is a tiny Windows tray program that notices when an app is using your microphone or webcam and tells the light, using the ESPHome Native API (encrypted, same protocol Home Assistant uses).
- **Home Assistant** is optional. If you have it, it discovers the light automatically and can use it alongside the PC app.

| Status   | Meaning                          |
|----------|----------------------------------|
| `free`   | Come in                          |
| `mic`    | Microphone is on                 |
| `camera` | Webcam is on                     |
| `call`   | In a call — please don't knock   |
| `dnd`    | Do not disturb (manual)          |
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
| `installer/` | MSI definition (built on Linux with wixl) |
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

- `KnockBlock-<version>.msi`: the Windows tray app. Installs per user, with no administrator rights; `AUTOSTART=0` skips starting with Windows.
- `knockblock-<version>.esphomebundle.tar.gz`: the firmware configuration. Extract it, fill in `secrets.yaml`, then run `esphome run knockblock.yaml`.

Details are in [`docs/release-notes.md`](docs/release-notes.md). Build them locally with `scripts/build-msi.sh` (needs `wixl` and `msitools`) and `scripts/build-firmware-bundle.sh` (needs ESPHome).

### The tray app

After installing the MSI, KnockBlock starts with Windows and sits in the notification area:

- The **icon colour** follows this PC's status: green free, amber microphone, orange camera, red call, purple do not disturb. It is grey while the light is unreachable.
- The **menu** shows the status and the connection. You can force "Free" or "Do not disturb" ("Automatic" goes back to detection), open the settings, toggle "Start with Windows", or quit.
- **Settings…** opens a small page in your browser (served on `127.0.0.1` only while it is open, behind a random per-session link; it closes itself after 3 minutes without use). It has the device host and port, the encryption key, the app and display languages, the PC name, whether to send app names, ignored apps, custom app names, notifications and "Start with Windows", plus a **Test connection** button. Everything is in your language (English or Italian; English is the fallback).
- The settings are stored in `%APPDATA%\KnockBlock\config.json`; the encryption key is encrypted with Windows DPAPI and never shown again once saved. The app reloads the file as soon as it changes. If you edit the file by hand, you can put the key in plain text as `"key": "…"`: the app encrypts it on the next start and removes the plain copy.
- **Logs** go to `%LOCALAPPDATA%\KnockBlock\logs\knockblock.log`. Run `knockblock.exe -debug -debug-mem 30s` to log details and memory use.

### Trying the PC side without the tray app

`knockctl` (a development tool, built by CI as `knockctl.exe`) runs the same presence loop as the tray app:

```sh
knockctl.exe -addr knockblock.local -key <api key> agent     # detect mic/camera use and report it until Ctrl-C
knockctl.exe detect                                            # show what Windows reports about mic/camera use
```

Options: `-pc NAME` (default: the computer name), `-ignore app.exe,…`, `-no-app-names`, `-debug-mem 30s`.

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
