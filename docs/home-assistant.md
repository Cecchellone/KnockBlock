# Home Assistant

Home Assistant is optional: the light works with the PC app alone. When
Home Assistant is there, it finds the light (ESPHome integration) and sees
the same entities as the PC app; it can also drive the light.

## Night mode and phone calls

Two switches let automations put the light in a state no PC knows about.
Both act like one more PC: the strongest status of all wins
(`docs/presence.md`).

| Switch | Status shown | Notes |
|--------|--------------|-------|
| `Night mode` | `night`: "Sleeping", dark blue, dimmed text and LED | kept across reboots; ranks above `dnd`, below a PC that records or films |
| `Phone call` | `call`: "In a call", phone icon and call duration | not kept across reboots; turns itself off after 4 h (`phone_call_max`) if no automation does |

The entity IDs below assume the device is named `knockblock`.

### Phone calls from the Android companion app

The Home Assistant companion app for Android has a **Phone state** sensor
(enable it in the app: Settings → Companion app → Manage sensors → Phone
state). It is `idle`, `ringing` or `offhook`; `offhook` means a call is in
progress. (The iOS app has no such sensor.)

```yaml
alias: KnockBlock – phone call
description: The light shows "In a call" while the phone is in a call.
triggers:
  - trigger: state
    entity_id: sensor.my_phone_phone_state  # your phone's sensor
actions:
  - action: >-
      {{ 'switch.turn_on' if trigger.to_state.state == 'offhook' else 'switch.turn_off' }}
    target:
      entity_id: switch.knockblock_phone_call
mode: queued
```

### Night mode on a schedule

```yaml
alias: KnockBlock – night mode
triggers:
  - trigger: time
    at: "23:30:00"
    id: "on"
  - trigger: time
    at: "08:00:00"
    id: "off"
actions:
  - action: switch.turn_{{ trigger.id }}
    target:
      entity_id: switch.knockblock_night_mode
mode: single
```

Other ideas: turn night mode on when the bedroom lights go off, or with a
"Good night" scene.

## Actions: any status, any message

Two actions show up in Home Assistant as `esphome.knockblock_set_status` and
`esphome.knockblock_display_message` (Developer tools → Actions, or in an
automation). Colours are `#RRGGBB` or a name: red, orange, yellow, green,
cyan, blue, purple, pink, white, black, grey; empty keeps the usual colour.

### `set_status`

A status like one more PC.

| Field | Meaning |
|-------|---------|
| `source` | who sets it; shown on the left of the info bar (`~something` shows nothing). Calling again with the same source replaces it |
| `status` | a status ID: `free`, `muted`, `mic`, `camera`, `call`, `dnd`, `night`; it decides the rank against the PCs and the icons. `offline` removes the source |
| `label` | text on the display instead of the usual label (empty: the usual one) |
| `colour` | display and LED colour (empty: the status colour) |
| `app` | shown in the centre of the info bar |
| `devices` | icons: `call`, `mic` or `mic_muted`, `camera`, comma-separated (empty: implied by the status) |
| `duration` | seconds it lasts; `0` until set `offline` |

```yaml
action: esphome.knockblock_set_status
data:
  source: Telefono
  status: call
  label: Al telefono
  colour: orange
  app: WhatsApp
  devices: call
  duration: 0
```

and to clear it:

```yaml
action: esphome.knockblock_set_status
data: {source: Telefono, status: offline, label: "", colour: "", app: "", devices: "", duration: 0}
```

### `display_message`

A message over the status, with a white bar at the bottom that shrinks as
its time runs out.

| Field | Meaning |
|-------|---------|
| `message` | the text (cut at 120 bytes; up to four lines) |
| `source` | shown on the left of the info bar |
| `app` | shown in the centre of the info bar |
| `colour` | background (empty: the status colour) |
| `duration` | seconds (at most 300); `0` from the length: 4 s + 0.2 s per character, 5 to 30 s |

```yaml
action: esphome.knockblock_display_message
data:
  message: La cena è pronta!
  source: Cucina
  app: ""
  colour: green
  duration: 0
```

Messages from the PC app (the doorbell reply) use the same automatic
duration.

## Other entities

- `select.knockblock_status` shows the combined status. Setting it reports a
  "manual" source, which expires after 2 minutes unless the
  `button.knockblock_heartbeat` is pressed; for lasting states use the
  switches above.
- `text_sensor` entities give the app, the PC and the devices in use
  (`active_app`, `active_pc`, `active_devices`).
- `event.knockblock_doorbell` fires on every ring.
