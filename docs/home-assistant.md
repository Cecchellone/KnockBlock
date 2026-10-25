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

## Other entities

- `select.knockblock_status` shows the combined status. Setting it reports a
  "manual" source, which expires after 2 minutes unless the
  `button.knockblock_heartbeat` is pressed; for lasting states use the
  switches above.
- `text_sensor` entities give the app, the PC and the devices in use
  (`active_app`, `active_pc`, `active_devices`).
- `event.knockblock_doorbell` fires on every ring.
