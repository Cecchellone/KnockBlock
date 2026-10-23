# ESPHome Native API: protocol notes

What the KnockBlock client (`client/internal/espapi`) implements, and where
each detail comes from. The protocol has no written specification beyond
`api.proto`. The reference is the Python library
[aioesphomeapi](https://github.com/esphome/aioesphomeapi), which Home
Assistant uses.

Pinned versions:
- ESPHome **2026.10.0** (`proto/VERSION`), API **1.19**.
- Behaviour checked against aioesphomeapi **46.21.0**.

## Transport

The client opens a TCP connection to port **6053**. Messages are protobuf
payloads; each message type has a numeric ID, set with the `(id)` option in
`api.proto` (for example HelloRequest = 1, PingRequest = 7). The client reads
these IDs from the generated descriptors, so none is written by hand.

### Plaintext frames

Source: `api.proto` ("BASE PACKETS") and
`aioesphomeapi/_frame_helper/plain_text.py`.

```
0x00 | varint(payload length) | varint(message type) | payload
```

- A varint has at most 4 bytes, and a frame at most 65535 bytes (the
  firmware's uint16 limit). Anything larger is a protocol error.
- A first byte of `0x01` means the device uses Noise encryption: the client
  returns `ErrRequiresEncryption`.

### Noise frames (milestone 4)

```
0x01 | uint16 BE(frame length) | frame
```

After the handshake, each frame is encrypted. The decrypted payload is
`uint16 BE(message type) | uint16 BE(payload length) | payload`. Source:
`aioesphomeapi/_frame_helper/noise.py`, `make_noise_packets` and
`_handle_frame`.

## Connection setup

1. TCP connect.
2. Noise handshake, only when a key is configured (milestone 4).
3. The client sends `HelloRequest{client_info, api_version_major: 1, api_version_minor: 19}` and waits for `HelloResponse`. The response carries `name` (the device name) and `server_info` (the ESPHome version).
   - Following aioesphomeapi (`connection.py`, `_process_hello_resp`), a response with major version > 2 is rejected (`ErrIncompatibleAPI`).
   - If a name is expected and differs, the client fails with `ErrBadName`.
4. Password authentication (`AuthenticationRequest`, IDs 3/4) was **removed
   in ESPHome 2026.1.0**. Nothing is sent after Hello.

Setup is bounded by 30 s (`CONNECT_REQUEST_TIMEOUT`) or the caller's context,
whichever ends first.

## Entities

- `ListEntitiesRequest`: the device answers with one `ListEntities*Response`
  per entity, then `ListEntitiesDoneResponse`. The client keeps the select,
  button and text sensor entities and skips the others.
- Commands address entities by `key` (fixed32), not by name.
- Entities are matched by **object ID**. ESPHome still sends `object_id`, but
  has announced its removal. When the field is empty, the client derives it
  from the name like `aioesphomeapi/object_id.py`, `compute_object_id`:
  spaces become `_`, A–Z become lowercase, anything outside `[a-z0-9_-]`
  becomes `_`. For example, "Display language" → `display_language`.
- `SubscribeStatesRequest`: the device sends the current state of every
  entity, then every change, including changes made by Home Assistant. Select
  and text sensor states carry `missing_state` when there is no valid state
  yet.
- `SelectCommandRequest{key, state}` and `ButtonCommandRequest{key}` have no
  response. The new state arrives as a state update.

## Keepalive

Source: `aioesphomeapi/connection.py`, `_async_send_keep_alive`.

- After 20 s without receiving anything, the client sends `PingRequest`. Any
  received frame counts as a sign of life.
- With no traffic for 4.5 × 20 s = 90 s, the client closes with `ErrTimeout`.
- The device (`api_connection.cpp`) pings after 60 s of silence and drops a
  client that stays silent for 150 s. The client answers every `PingRequest`
  with `PingResponse`.

## Disconnect

- Client side: send `DisconnectRequest` and wait up to 10 s for
  `DisconnectResponse` (`DISCONNECT_RESPONSE_TIMEOUT`), then close the
  socket.
- Device side: on `DisconnectRequest`, the client answers `DisconnectResponse`
  and closes (`_handle_disconnect_request_internal`). The error is
  `ErrDisconnected`. Since 2026.x the request may carry a reason (for example
  `DISCONNECT_REASON_PROVISIONING_CLOSED`).

## Unknown messages

Message types the client does not handle are skipped, as aioesphomeapi does.
For example, the device may send `DeviceInfoResponse`, or `GetTimeRequest`
when it uses Home Assistant time.

## Testing

- **Unit tests** (`go test ./internal/espapi/`):
  - framing vectors produced with aioesphomeapi's `make_plain_text_packets`;
  - a fuzz test of the frame reader;
  - connection tests against an in-process fake device (`fakedevice_test.go`).
- **Integration tests** (`scripts/itest-host.sh`): the same client against
  `firmware/host-test.yaml` compiled for ESPHome's host platform. They check
  status changes, localized labels and the watchdog. They run in CI and on
  any Linux machine with ESPHome installed.
