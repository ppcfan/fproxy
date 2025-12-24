# Design: Bidirectional UDP Relay

## Context

The fproxy UDP relay currently operates in a single direction:
```
Source UDP -> Client -> Server -> Target UDP
```

For bidirectional support, we need to add the reverse path:
```
Target UDP -> Server -> Client -> Source UDP
```

## Core Principle

**One source UDP socket = One Session = One target UDP socket**

This ensures responses from the target can be correctly routed back to the originating source.

## Goals / Non-Goals

### Goals
- Enable responses from target server to reach the original source
- Correctly route responses when multiple sources are active concurrently
- Support session timeout for resource cleanup
- Work with both UDP and TCP transport between client and server

### Non-Goals
- NAT traversal for complex network topologies
- Guaranteed delivery (still UDP)
- Session persistence across restarts

## Decisions

### Decision 1: Session Definition

**What**: A session is defined by the source UDP address:port.

- Client maintains: `map[sourceAddr] -> session`
- Server maintains: `map[sessionID] -> targetSession` (UDP) or `map[tcpConn] -> targetSession` (TCP)

**Rationale**: Each source address needs its own target socket to correctly receive responses.

### Decision 2: Protocol Format

**What**: Different formats for UDP and TCP transport. All multi-byte integers use big-endian byte order.

**UDP transport**:
```
[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]
Header size: 8 bytes
```

**TCP transport**:
```
Protocol layer: [4-byte seq (big-endian)][payload]
Header size: 4 bytes

TCP framing (handled by sender/receiver): [2-byte length (big-endian)][protocol data]
```

**Rationale**:
- UDP transport multiplexes multiple sessions over one connection, needs session_id
- TCP transport uses one connection per session, session_id is implicit
- Big-endian is network byte order, consistent with existing seq encoding

### Decision 3: Per-Session Deduplication

**What**: Each session maintains its own deduplication window.

Current (broken for multi-session):
```
Global DedupWindow tracks all seq values
Problem: Different sessions can have same seq, causing false duplicate detection
```

New design:
```
UDP: map[sessionID] -> DedupWindow
TCP: map[tcpConn] -> DedupWindow
```

**Rationale**:
- seq is per-session (each session has its own counter starting at 0)
- Without per-session dedup, seq=0 from session A would block seq=0 from session B

### Decision 4: Per-Session Sequence Counter

**What**: Client maintains a separate seq counter for each session.

```go
type ClientSession struct {
    sessionID    uint32      // for UDP transport
    seq          atomic.Uint32
    lastActivity time.Time
}
```

**Rationale**: Each session's seq starts at 0 and increments independently.

### Decision 5: Server Target Socket Management

**What**: Server creates a dedicated UDP socket to target for each session.

```go
type TargetSession struct {
    targetConn   *net.UDPConn
    dedupWindow  *DedupWindow
    lastActivity time.Time
    cancel       context.CancelFunc
}
```

For UDP transport: `map[sessionID] -> *TargetSession`
For TCP transport: `map[tcpConn] -> *TargetSession`

**Rationale**: Using separate sockets allows the OS to route responses back to the correct session.

### Decision 6: Session Timeout and Cleanup

**What**: Sessions expire after configurable timeout (default 60s).

Cleanup includes:
- Close target socket
- Remove from session map
- Remove dedup window

Cleanup triggered by:
- Periodic sweep (every 10s)
- On shutdown

**Rationale**: Prevents resource leaks from abandoned sessions.

## Data Flow

### Request Flow (UDP transport)
```
Source:5000 ──UDP──> Client:3000
                        │
                        ├─ Lookup/create session for Source:5000
                        │  sessionID = 0x00000001, seq = 0
                        │
                        └──[sid=1][seq=0][payload]──> Server:8001
                                                          │
                                                          ├─ Lookup/create target session for sid=1
                                                          │  Create new targetConn, new DedupWindow
                                                          │  Dedup check: (sid=1, seq=0) not seen -> accept
                                                          │
                                                          └──[payload]──> Target:9000
```

### Response Flow (UDP transport)
```
Target:9000 ──[response]──> Server:8001 (via targetConn for sid=1)
                              │
                              ├─ Lookup session from targetConn
                              │  sessionID = 0x00000001
                              │
                              └──[sid=1][seq=0][response]──> Client:3000
                                                                 │
                                                                 ├─ Decode sid=1, lookup source addr
                                                                 │  sourceAddr = Source:5000
                                                                 │
                                                                 └──[response]──> Source:5000
```

### TCP Transport Flow
```
Source:5000 ──UDP──> Client:3000
                        │
                        ├─ Lookup/create TCP conn for Source:5000
                        │  Create tcpConn to Server:8001, seq = 0
                        │
                        └──[len][seq=0][payload]──TCP──> Server:8001
                                                            │
                                                            ├─ Lookup/create target session for this tcpConn
                                                            │  Create new targetConn, new DedupWindow
                                                            │
                                                            └──[payload]──> Target:9000

(Response follows reverse path via same tcpConn and targetConn)
```

## Deduplication Example

```
Session A (sid=1): seq 0, 1, 2, ...
Session B (sid=2): seq 0, 1, 2, ...

Server receives:
  [sid=1][seq=0] -> DedupWindow[1].IsDuplicate(0) = false -> accept
  [sid=2][seq=0] -> DedupWindow[2].IsDuplicate(0) = false -> accept (different window!)
  [sid=1][seq=0] -> DedupWindow[1].IsDuplicate(0) = true  -> reject (duplicate)
```

## Risks / Trade-offs

### Risk: Target Socket Exhaustion
- **Cause**: Too many concurrent sessions
- **Mitigation**: Configurable max sessions limit, aggressive timeout

### Risk: Session ID Collision
- **Cause**: 32-bit ID space exhaustion
- **Mitigation**: Use atomic counter, 4 billion IDs should be sufficient; recycle on timeout

### Trade-off: Memory Usage
- Each session needs a target socket, goroutine, and dedup window
- Acceptable for typical use cases (hundreds to thousands of concurrent sources)

## Open Questions

None - design is clear.
