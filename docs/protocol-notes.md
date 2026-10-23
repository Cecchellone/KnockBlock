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

### Noise encryption

Selected when an encryption key is configured. Sources:
`aioesphomeapi/_frame_helper/noise.py`, `noise_encryption.py`, and the device
side, `esphome/components/api/api_frame_helper_noise.cpp`.

- Protocol: `Noise_NNpsk0_25519_ChaChaPoly_SHA256` (flynn/noise).
- PSK: the base64 key from `api: encryption: key:`, which decodes to 32 bytes.
  Anything else is `ErrInvalidKeyFormat`.
- Prologue: `NoiseAPIInit` followed by the 2-byte length of the empty
  ClientHello frame, i.e. `NoiseAPIInit\x00\x00`. The device builds it from
  the frame it received; aioesphomeapi hard-codes it.
- Every frame: `0x01 | uint16 BE(length) | data`.

Handshake:

```
client → 01 00 00                         empty ClientHello frame
client → 01 len | 00 | Noise message 1    (sent in the same write)
device → 01 len | 01 | name 00 | mac 00   ServerHello; 0x01 = the protocol above
device → 01 len | 00 | Noise message 2    handshake response
         01 len | 01 | reason             or a rejection
```

- The name in ServerHello exists since ESPHome 2022.2, the MAC since 2025.4.
  If a name is expected, it is checked here, before any key material is
  used.
- The rejection reason `Handshake MAC failure` means the key is wrong
  (`ErrInvalidKey`). So does a response that fails to authenticate. Any other
  reason is `ErrHandshake`.
- A device without encryption answers with plaintext frames (first byte
  `0x00`), giving `ErrDevicePlaintext`. Some devices simply close the
  connection instead (`ErrClosed`, with a hint).

After the handshake, each frame carries a ChaChaPoly ciphertext. The decrypted
data is `uint16 BE(message type) | uint16 BE(payload length) | payload`. As in
aioesphomeapi (`_handle_frame`), the inner length is not trusted: the payload
is the rest of the decrypted data, which must be at least 4 bytes. A frame that
fails to decrypt closes the connection (`ErrProtocol`).

## Connection setup

1. TCP connect.
2. Noise handshake, only when a key is configured (see above).
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
- **Noise vectors** (`testdata/noise_vectors.json`): a complete exchange
  recorded with fixed ephemeral keys by the Python `noise` library, which is
  what aioesphomeapi uses (`testdata/gen_noise_vectors.py`). The Go client
  reproduces it byte for byte: handshake, ServerHello parsing, one encrypted
  frame in each direction.
- **Interop with aioesphomeapi** (`TestInteropFakeDevice`,
  `testdata/interop_aioesphomeapi.py`): the reference client connects over
  Noise to the fake device built on this package's transport. It completes
  the handshake, Hello, entity listing and commands. With a wrong key it
  reports `InvalidEncryptionKeyAPIError`.
- **Integration tests** (`scripts/itest-host.sh`): the same client against
  `firmware/host-test.yaml` compiled for ESPHome's host platform. They check
  status changes, localized labels and the watchdog. They run in CI and on
  any Linux machine with ESPHome installed.
  - `firmware/host-test-noise.yaml` runs the same tests encrypted, plus
    checks that a wrong key and a missing key are rejected
    (`KNOCKBLOCK_ITEST_KEY`).
  - The encrypted host build fetches noise-c and libsodium from the
    PlatformIO registry, so it needs that registry to be reachable (CI is).
