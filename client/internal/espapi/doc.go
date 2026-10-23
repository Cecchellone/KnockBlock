// Package espapi is a minimal client for the ESPHome Native API.
//
// It implements only what KnockBlock needs (CLAUDE.md §5.1): plaintext and
// Noise framing, the Hello handshake, ListEntities, SubscribeStates, select
// and button commands, keepalive pings and graceful disconnect. It is not a
// general-purpose library. Protocol details mirror the reference Python
// implementation aioesphomeapi; see docs/protocol-notes.md.
package espapi
