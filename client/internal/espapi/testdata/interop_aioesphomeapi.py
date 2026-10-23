"""Connects aioesphomeapi (the reference client) to the Go fake device over
Noise; see TestInteropFakeDevice. Usage, from client/:

    KNOCKBLOCK_INTEROP_ADDR=127.0.0.1:16053 KNOCKBLOCK_INTEROP_KEY=<key> \
        go test -run TestInteropFakeDevice ./internal/espapi/ &
    python internal/espapi/testdata/interop_aioesphomeapi.py 127.0.0.1 16053 <key>
"""

import asyncio
import sys

from aioesphomeapi import APIClient


async def main(host: str, port: int, key: str) -> None:
    client = APIClient(host, port, None, noise_psk=key)
    await client.connect(login=True)
    entities, _ = await client.list_entities_services()
    by_id = {e.object_id: e for e in entities}
    assert {"status", "display_language", "heartbeat", "status_label"} <= by_id.keys(), by_id
    states = {}
    client.subscribe_states(lambda s: states.__setitem__(s.key, getattr(s, "state", None)))
    client.select_command(by_id["status"].key, "call")
    client.select_command(by_id["display_language"].key, "it")
    client.button_command(by_id["heartbeat"].key)
    for _ in range(50):
        if states.get(by_id["status"].key) == "call" and states.get(by_id["display_language"].key) == "it":
            break
        await asyncio.sleep(0.1)
    assert states.get(by_id["status"].key) == "call", states
    await client.disconnect()
    print("interop OK:", sorted(by_id))


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1], int(sys.argv[2]), sys.argv[3]))
