# Change: Add Bidirectional UDP Relay Support

## Why

The current UDP relay implementation is unidirectional - it forwards packets from source to target but does not relay responses back. Many UDP-based protocols (DNS, game protocols, VoIP, etc.) require bidirectional communication where the source must receive responses from the target server.

## What Changes

### Core Principle
**One source UDP socket = One Session = One target UDP socket**

This ensures responses from the target can be correctly routed back to the originating source.

### Protocol Changes

| Transport | Protocol Layer Format | Notes |
|-----------|----------------------|-------|
| UDP | `[4B session_id][4B seq][payload]` | All fields big-endian, header = 8 bytes |
| TCP | `[4B seq][payload]` | Big-endian, header = 4 bytes; TCP sender adds 2B length prefix for framing |

### Deduplication Changes
- Current: global dedup by seq only
- New: per-session dedup by seq
  - UDP transport: dedup per session_id
  - TCP transport: dedup per TCP connection
- Each session has its own `DedupWindow`

### Client Changes
- Track sessions by source UDP address
- For UDP transport: generate session_id, include in packet header, per-session seq counter
- For TCP transport: create separate TCP connection per source address, per-session seq counter
- Add response receiver goroutines to forward responses back to source

### Server Changes
- Create dedicated target UDP socket per session
- Create dedicated DedupWindow per session
- For UDP transport: map session_id -> (target socket, dedup window)
- For TCP transport: map TCP connection -> (target socket, dedup window)
- Read responses from each target socket, route back to client
- Implement session timeout to close idle target sockets and clean up dedup state

### Configuration
- Add `session_timeout` option (default: 60s)

## Impact

- Affected specs: `udp-relay`
- Affected code:
  - `protocol/packet.go` - Add `EncodeUDP`/`DecodeUDP`, rename existing to `EncodeTCP`/`DecodeTCP`
  - `client/client.go` - Session tracking, per-session seq, response handling
  - `server/server.go` - Per-session target sockets, per-session dedup, response forwarding
  - `config/config.go` - Session timeout configuration
