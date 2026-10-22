# KnockBlock

*Like an ad blocker, but for people knocking on your door.*

KnockBlock is a small status light that hangs outside your room and tells everyone in the house whether you are free, have the microphone on, have the webcam on, or are in a call. Your PC updates it automatically, so you never have to remember to flip a sign again.

> 🚧 **Work in progress.** Nothing here is ready to use yet. See [`CLAUDE.md`](CLAUDE.md) for the design and the roadmap.

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

## License

TBD.
