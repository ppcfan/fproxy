## Context
fproxy needs UDP forwarding with reliability improvements. The design uses a client-server relay model where:
- Client receives UDP packets locally, assigns sequence numbers, and sends to multiple server endpoints
- Server receives from multiple paths, deduplicates by sequence number, and forwards to target

This is a new architectural pattern for the project.

## Goals / Non-Goals
- Goals:
  - Reliable UDP forwarding through redundant paths
  - Support UDP and TCP as transport between client and server
  - Simple configuration via CLI or config file
  - Packet deduplication on server side
- Non-Goals:
  - Encryption (can be added later)
  - Compression
  - Session management or connection tracking
  - Retransmission or acknowledgment

## Decisions

### Packet Protocol
- Decision: Use a simple binary header: `[4-byte sequence number][payload]`
- Rationale: Minimal overhead, easy to implement, sufficient for deduplication
- Alternatives considered:
  - Protobuf: Overkill for simple sequence + payload
  - JSON: Too much overhead for high-throughput UDP

### Deduplication Strategy
- Decision: Use a fixed-size sliding window with a map of seen sequence numbers
- Rationale: Bounded memory usage, handles out-of-order packets within window
- Window size: Configurable, default 10000 packets
- Alternatives considered:
  - Bloom filter: False positives unacceptable
  - Unbounded map: Memory leak risk

### Configuration
- Decision: Support both CLI flags and YAML config file
- CLI flags take precedence over config file
- Config file path specified via `--config` flag

### Architecture
```
┌─────────────────┐     UDP/TCP      ┌─────────────────┐     UDP      ┌─────────────┐
│  UDP Source     │ ──► │  Client     │ ══════════════► │  Server     │ ──────────► │  Target     │
│  (application)  │     │  (listen)   │   multi-path    │  (listen)   │             │  (service)  │
└─────────────────┘     └─────────────┘                 └─────────────┘             └─────────────┘
```

### Package Structure
```
fproxy/
├── main.go           # Entry point, CLI parsing
├── config/
│   └── config.go     # Configuration loading
├── protocol/
│   └── packet.go     # Packet encoding/decoding
├── client/
│   └── client.go     # Client mode implementation
└── server/
    └── server.go     # Server mode implementation
```

## Risks / Trade-offs
- Risk: Sequence number wraparound after 2^32 packets → Mitigation: 32-bit sequence is sufficient for most use cases; can extend to 64-bit if needed
- Risk: Memory usage with large dedup window → Mitigation: Configurable window size, periodic cleanup
- Trade-off: No encryption → Simpler implementation, can layer TLS/encryption later

## Migration Plan
- This is a new feature, no migration needed
- Existing code (main.go hello world) will be replaced

## Open Questions
- None currently - requirements are clear
