# CLAUDE.md — KnockBlock

Guidance for Claude (and humans) working in this repository. Read this file fully before making changes.

## 1. What this project is

**KnockBlock** — *like an ad blocker, but for people knocking on your door* — is a "do not disturb" status light that hangs outside a bedroom door. It tells people in the house whether the owner is free, has the microphone on, has the webcam on, or is in a call.

It has two parts:

1. **Firmware** — an ESPHome configuration for a **Waveshare ESP32-C6-LCD-1.47** board (ESP32-C6, 1.47" ST7789 LCD 172×320, onboard WS2812 RGB LED). The device shows the current status with a colour on the LED and a localized label on the display.
2. **Windows tray app** — a small, always-on Go program that detects microphone/webcam usage on the PC and sends the status to the device over the **ESPHome Native API**.

Home Assistant is **optional**. The device must work fully with only the PC app. When Home Assistant is present, it connects to the same Native API at the same time and sees the same entities.

Planned later (do not implement yet, but do not block them): IR transmitter to control an old air conditioner (`remote_transmitter` + `climate_ir`), DHT22 temperature/humidity sensor.

## 2. Non-negotiable requirements

These three requirements drive every design decision. If a change conflicts with one of them, stop and ask.

### 2.1 Low RAM footprint

- Target: **≤ 15 MB private working set** for the tray app when idle (measured in Task Manager, "Memory" column). Treat 20 MB as a hard ceiling.
- No GUI framework resident in memory. No Qt, no Fyne widgets, no WebView, no Electron.
- The tray icon uses `fyne.io/systray` (pure Go on Windows, no CGO).
- The settings UI is a small HTML page served by `net/http` on `127.0.0.1` **only while open**, opened in the user's default browser from the tray menu, and shut down after it closes or after a short inactivity timeout.
- Build with `CGO_ENABLED=0`. Every new dependency must be justified in the PR description; prefer the standard library and `golang.org/x/...`.
- Avoid per-tick allocations in the detection loop (reuse buffers, no fresh maps/slices every poll). Logs go to a rotating file, never kept in memory.
- Add a simple way to check memory (e.g. a debug flag that logs `runtime.MemStats` periodically) and report numbers in PRs that touch the runtime path.

### 2.2 Native connection

- The PC app talks to the device **only through the ESPHome Native API** (TCP 6053, protobuf messages, Noise encryption). Do **not** enable or use ESPHome's `web_server` REST API.
- There is no well-maintained Go library for the Native API, so we write a **minimal internal client** (`client/internal/espapi`). It is not a general-purpose library: implement only the messages the app needs (see §5).
- Protobuf types are generated from ESPHome's `api.proto` and `api_options.proto`, **pinned to a specific ESPHome version** stored in `proto/VERSION`. Regenerate with a script, never hand-edit generated code.
- Encryption uses Noise `Noise_NNpsk0_25519_ChaChaPoly_SHA256` with the device's base64 PSK (`api: encryption: key:`). Use `github.com/flynn/noise`.
- The official reference implementation is the Python library **`aioesphomeapi`**. When a protocol detail is unclear (handshake bytes, prologue, message ordering, ping handling), copy the behaviour from its source, and cite the file/function in a code comment.
- Plaintext transport must also be supported (useful for tests and as a fallback), selected automatically when no key is configured.

### 2.3 Multilingual

- **All repository content is in English**: code, identifiers, comments, commit messages, docs, issue/PR text.
- **Everything user-facing is localized**, with **English and Italian** shipped from day one, and adding a new language must require **no code changes** — only new locale files (plus, for firmware, one new entry where languages are listed).
- English is the fallback for any missing key or language.
- This applies to: tray menu and tooltips, notifications, the settings page, installer, and the **text shown on the device display**.
- Details in §6.

## 3. Repository layout (target)

```
/CLAUDE.md
/README.md                      # user-facing: what it is, how to flash, how to install
/LICENSE
/.gitignore                     # must ignore secrets.yaml, build output, *.exe
/firmware/
  knockblock.yaml                 # main ESPHome config for the real board (local build)
  knockblock-package.yaml         # same firmware as a remote package (fetched from GitHub)
  examples/                       # device configs that use the remote package
  host-test.yaml                # same logic on ESPHome's `host` platform, for tests without hardware
  common/                       # shared packages: core.yaml (status logic, watchdog, entities), board.yaml (hardware)
  components/                   # local external components (knockblock_i18n: display labels)
  locales/                      # display strings, one file per language (see §6.2)
  secrets.example.yaml          # template; real secrets.yaml is git-ignored
/proto/
  VERSION                       # pinned ESPHome version, e.g. 2026.10.0
  api.proto
  api_options.proto
  generate.sh                   # regenerates Go code into client/internal/espapi/pb
/client/                        # Go module
  go.mod
  cmd/knockblock/                 # main package: tray app entry point
  cmd/knockctl/                   # dev CLI on the same client (tests, trying the device); not shipped
  internal/espapi/              # minimal Native API client (transport, noise, framing, messages)
  internal/espapi/pb/           # generated protobuf code (do not edit)
  internal/detect/              # mic/webcam detection (Windows impl + fake for tests)
  internal/status/              # state machine: inputs -> status ID
  internal/config/              # config load/save, secret protection
  internal/i18n/                # locale loading, language detection, fallback
  internal/settingsui/          # on-demand localhost settings page (html/template, embedded assets)
  internal/tray/                # tray icon, menu, icons per status
  internal/memdebug/            # periodic memory statistics (debug flag, §2.1)
  locales/                      # en.json, it.json (embedded with go:embed)
/installer/
  knockblock.iss                  # Inno Setup script (English + Italian)
/docs/
  protocol-notes.md             # what we learned about the Native API, with aioesphomeapi references
  adding-a-language.md          # step-by-step guide
```

## 4. Firmware design (ESPHome)

- Target board: Waveshare ESP32-C6-LCD-1.47. Use `esp32: variant: esp32c6` with `framework: type: esp-idf`.
- **Verify before writing pins**: display (SPI pins, CS, DC, RST, backlight), LED GPIO (believed to be GPIO8), panel dimensions and offsets for the ST7789 172×320. Take them from the official Waveshare wiki/schematic and note the source URL in a YAML comment. Do not guess: a wrong pin means a black screen.
- Entities exposed over the Native API (names in English, stable IDs):
  - `select` **`status`** — options are **language-neutral status IDs**: `free`, `mic`, `camera`, `call`, `dnd`, `offline`. It shows the **combined** status of all sources (see Presence below) and updates LED colour and display label. Setting it directly (Home Assistant) reports the "manual" source.
  - `select` **`display_language`** — options are language codes (`en`, `it`, …); persisted across reboots (`restore_value: true`).
  - API action **`report_presence(pc, status, app)`** — called by every PC app on change and at least every ~30 s.
  - `text_sensor` **`active_app`**, **`active_pc`** — app and PC causing the current status; **`status_label`** — the localized label shown.
  - `button` **`heartbeat`** — keeps the manual source alive.
  - `switch` **`privacy_mode`** (board) — hides app and PC names on the display.
  - Optional: `light` for the RGB LED so Home Assistant can use it directly; `number` for display brightness.
- **Presence and watchdog** (`docs/presence.md`): several PCs may drive the light at once. Each source (a PC, or the manual source) expires after ~2 minutes without a report, so the light cannot stay on "call" after a PC shuts down. The strongest live status wins (`call` > `camera` > `mic` > `dnd` > `free`); with no live source the status is `offline`. Do not rely on `api.connected`, because Home Assistant counts as a client too.
- `api:` with `encryption: key: !secret api_encryption_key` and **`reboot_timeout: 0s`** (otherwise the device reboots in a loop when neither the PC nor Home Assistant is connected).
- `ota:` and `logger:` enabled; no `web_server`.
- Fonts: the display font **must include accented glyphs** used by supported languages (at least `àèéìòù ÀÈÉÌÒÙ`). When a language is added, extend the glyph set.
- `host-test.yaml` must reuse the same packages as the real board (minus display/LED hardware) so the Go client can be integration-tested against a real ESPHome firmware running on Linux.

## 5. Tray app design (Go, Windows)

### 5.1 Native API client — minimal scope

Implement, in this order:

1. Framing: plaintext (`0x00`, varint length, varint type, payload) and Noise (`0x01`, 2-byte big-endian length, encrypted payload; the decrypted payload starts with a 2-byte type and a 2-byte length).
2. Noise handshake exactly as `aioesphomeapi` does it (including prologue and the server-hello frame).
3. Hello / connection negotiation as done by the current `aioesphomeapi` for the pinned ESPHome version.
4. `ListEntities` → map entity `object_id`/name to numeric `key` (commands use keys).
5. `SubscribeStates` → track current `status` and `display_language` values (they may be changed from Home Assistant).
6. Commands: `SelectCommandRequest`, `ButtonCommandRequest`, `ExecuteServiceRequest` (the `report_presence` action; string arguments only).
7. Keepalive: answer device `PingRequest`; send our own pings; detect dead connections.
8. Graceful `DisconnectRequest`/`DisconnectResponse`.
9. Reconnect with exponential backoff and jitter, forever, silently.

Design rules: context-aware APIs, one goroutine reading, writes serialized, no global state, typed errors. Unit-test framing and Noise against recorded vectors; integration-test against `host-test.yaml`.

### 5.2 Detection

- Source: `HKCU\Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\{microphone,webcam}`, including the `NonPackaged` subkey (desktop apps; executable path encoded with `#` instead of `\`).
- An app is "in use" when `LastUsedTimeStop == 0` and `LastUsedTimeStart != 0`. Verify this on a real machine and document findings in `docs/`.
- Poll every 1–2 s (simple and cheap). Event-based `RegNotifyChangeKeyValue` is an optional later optimization.
- User-configurable **ignore list** by executable name (e.g. apps that keep the mic open outside calls).
- Report **which app** uses the camera/microphone, using friendly names from a built-in table that the settings can extend (`docs/presence.md`). Browsers show as the browser.
- Windows-specific code behind `//go:build windows`; a fake implementation for tests so the module builds and tests on Linux.

### 5.3 Status logic

- `mic && camera` → `call`; `camera` only → `camera`; `mic` only → `mic`; none → `free`.
- Manual override from the tray menu (`dnd`, `free`, auto) takes priority until cleared.
- Send `report_presence(pc, status, app)` on change and every ~30 s; `pc` defaults to the Windows computer name (configurable). Send `offline` on exit. A setting can omit app names.
- On resume from sleep / network change: force reconnect and resend status immediately.

### 5.4 UX

- Tray icon colour reflects status; tooltip shows localized status and connection state.
- Menu: current status, override options, "Open settings", "Start with Windows" toggle, "Quit".
- Notifications: only for things that matter (device unreachable for several minutes), and they can be disabled.
- Settings page: device host (default `knockblock.local`), port, encryption key, language (auto / English / Italian / …), device display language, ignore list, connection test. Protect it with a random per-session token in the URL; bind only to `127.0.0.1`.

### 5.5 Config and secrets

- Config file in `%APPDATA%\KnockBlock\config.json`.
- The encryption key is stored encrypted with Windows DPAPI (`CryptProtectData`, available via `golang.org/x/sys/windows`). Never log it.
- Autostart via `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`.

### 5.6 Build and distribution

- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -H=windowsgui"`.
- Embed icon and version info in the executable.
- Installer with Inno Setup (`installer/knockblock.iss`), offering English and Italian during setup, with optional "Start with Windows".

## 6. Internationalization

### 6.1 Tray app

- Locale files: `client/locales/<lang>.json` (BCP 47 codes: `en`, `it`, later `de`, `pt-BR`, …), embedded with `go:embed`. Flat keys, e.g. `"status.call": "In call"`, `"menu.quit": "Quit"`.
- `en.json` is the source of truth; every key must exist there.
- Language selection: user setting → Windows UI language (`GetUserDefaultUILanguage`) → `en`. Match `it-IT` to `it`.
- Implement a tiny in-house loader (no heavy i18n framework) with simple `{name}` placeholder substitution. Plural forms are not needed now; do not add them unless a string requires it.
- The list of available languages is discovered from the embedded files, so adding a file is enough.
- A unit test fails if any locale file has keys missing from or extra to `en.json`, or broken placeholders.

### 6.2 Firmware display

- Status labels live in `firmware/locales/<lang>.yaml`, one file per language, keyed by status ID.
- The active language is chosen at runtime with the `display_language` select; unknown/missing labels fall back to English.
- Adding a language = add `firmware/locales/<lang>.yaml`, add the code to the `display_language` options, extend font glyphs if needed. Document the exact steps in `docs/adding-a-language.md`.
- Entity names exposed to Home Assistant stay in English; status values are IDs, not translated text.

### 6.3 Installer and docs

- Inno Setup: include `English` and `Italian` language entries.
- `README.md` is in English; a translated `README.it.md` is welcome but optional.

## 7. Milestones

Work in small PRs, one milestone (or part of one) at a time. Each milestone has acceptance criteria.

1. **Repo skeleton** — layout from §3, `.gitignore`, `go.mod`, CI (GitHub Actions: `gofmt -l`, `go vet`, `staticcheck`, `go test ./...` on Linux, Windows cross-build).
2. **Firmware v1** — `knockblock.yaml` + `host-test.yaml`, status select, language select, heartbeat + watchdog, en/it labels. Accept: `esphome config` passes for both files; pin choices cite the Waveshare source.
3. **Proto + client (plaintext)** — pinned proto, generator script, framing, hello, list entities, select/button commands, ping. Accept: integration test changes `status` on the `host` device.
4. **Noise encryption** — handshake + encrypted transport. Accept: same integration test passes with an encryption key; unit tests with fixed vectors.
5. **Detection + status logic** — registry reader, ignore list, state machine, fake for tests. Accept: unit tests for the state machine; manual test notes on Windows.
6. **Tray app** — systray, menu, reconnect/backoff, sleep/resume handling, heartbeat, i18n wired in. Accept: idle memory ≤ 15 MB reported in the PR.
7. **Settings page + config** — on-demand localhost UI, DPAPI secret storage, autostart toggle, localized.
8. **Installer + release** — Inno Setup script, GitHub Actions release artifact.

## 8. Environment notes for cloud sessions

- Cloud sessions run on Linux. The Go module must build and test on Linux; Windows-only code is behind build tags with fakes. Cross-compile the Windows binary to verify it builds, but it cannot be run or visually tested here.
- ESPHome can be installed with `pip install esphome` (may need `--break-system-packages`). Use `esphome config` to validate YAML. Compiling for the ESP32-C6 may fail if the toolchain cannot be downloaded; the `host` platform build is the priority for tests.
- Never flash hardware or assume access to the user's network. Hardware testing is done by the user.
- If network access blocks a step, say so clearly in the PR instead of working around it.

## 9. Conventions

- Go: `gofmt`, `go vet`, `staticcheck` clean; table-driven tests; errors wrapped with `%w`; no panics in library code.
- YAML: ESPHome style, comments in English, secrets only via `!secret`.
- Commits: Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `chore:`).
- Every PR: what changed, how it was tested, memory impact if the runtime path changed, anything that still needs verification on real hardware or Windows.
- When unsure about a protocol detail or hardware pin, **do not guess** — leave a `TODO(verify):` comment with what needs checking and where to look.

## 10. Out of scope (for now)

- macOS/Linux tray apps (keep the code portable where it is free, but do not build them).
- REST/`web_server`, MQTT, Matter, Bluetooth.
- IR air-conditioner control and DHT22 (planned; keep firmware modular so they slot in as packages).
- A general-purpose Go library for the ESPHome Native API.
