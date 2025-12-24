# Change: Unified Session Protocol for TCP and UDP Transport

## Why
The original protocol design used different packet formats for TCP and UDP transport:
- UDP: `[sessionID][seq][payload]`
- TCP: `[seq][payload]` (session identified by TCP connection)

This caused issues with multi-path redundancy where the same session needs to be tracked across both UDP and TCP paths. Unified session tracking enables:
- Cross-path deduplication (same session across UDP and TCP)
- Multi-path response redundancy (responses sent via all paths)
- Simplified session management

## What Changes
- **BREAKING**: TCP packet format now matches UDP: `[sessionID][seq][payload]`
- **BREAKING**: TCP header size increased from 4 bytes to 8 bytes
- **BREAKING**: Server TCP deduplication now uses (sessionID, seq) instead of per-connection seq
- **BREAKING**: TCP responses now include sessionID for routing
- Client TCP path now shares session state with UDP path
- Session cleanup considers activity from both TCP and UDP paths

## Impact
- Affected specs: `specs/udp-relay/spec.md`
- Affected code: `protocol/packet.go`, `client/client.go`, `server/server.go`
- **Breaking change**: Clients and servers using old protocol are incompatible

## Migration
This is a breaking protocol change. Both client and server must be upgraded together. There is no backward compatibility path.
