## Context

fproxy is a Layer 4 bidirectional UDP relay with multi-path transmission and deduplication. The current implementation is in Go (~1200 lines across 4 packages). This document outlines the technical decisions for rewriting it in Rust while maintaining functional parity.

### Stakeholders
- Users running fproxy in production (need seamless migration)
- Developers maintaining the codebase

### Constraints
- Must maintain wire protocol compatibility
- Must maintain configuration file compatibility
- Must maintain CLI flag compatibility
- No new dependencies on external services

## Goals / Non-Goals

### Goals
- 1:1 functional parity with Go implementation
- Idiomatic Rust code following community best practices
- Comprehensive test coverage matching existing tests
- Clear module boundaries matching current package structure

### Non-Goals
- Performance optimization beyond what Rust provides naturally
- Adding new features or capabilities
- Supporting additional platforms beyond current Go support
- GUI or web interface

## Decisions

### Decision 1: Async Runtime - Tokio

**Choice**: Use Tokio as the async runtime

**Rationale**:
- Industry standard for async Rust networking
- Excellent UDP and TCP support via `tokio::net`
- Built-in synchronization primitives (`tokio::sync`)
- Well-documented with large community
- Supports graceful shutdown patterns

**Alternatives considered**:
- `async-std`: Less mature, smaller ecosystem
- `smol`: Lighter weight but less feature-rich
- Blocking I/O with threads: Would lose benefits of async, higher memory per connection

### Decision 2: Project Structure

**Choice**: Mirror the Go package structure with Rust modules

```
fproxy-rs/
├── Cargo.toml
├── src/
│   ├── main.rs           # Entry point
│   ├── lib.rs            # Library root
│   ├── config.rs         # Configuration (config package)
│   ├── protocol.rs       # Packet encoding/decoding (protocol package)
│   ├── client.rs         # Client mode (client package)
│   └── server.rs         # Server mode (server package)
└── tests/
    └── integration_test.rs
```

**Rationale**:
- Maintains familiarity for developers
- Clear separation of concerns
- Easy to compare implementations side-by-side

### Decision 3: Dependency Choices

| Go Package | Rust Crate | Notes |
|------------|------------|-------|
| `log/slog` | `tracing` + `tracing-subscriber` | Structured logging with async support |
| `gopkg.in/yaml.v3` | `serde` + `serde_yaml` | De-facto standard for serialization |
| `flag` | `clap` (derive) | Feature-rich CLI parsing with derive macros |
| `net` | `tokio::net` | Async UDP/TCP sockets |
| `sync` | `tokio::sync` + `std::sync` | RwLock, Mutex, channels |
| `testing` | Built-in + `tokio::test` | Native test framework |

### Decision 4: Error Handling

**Choice**: Use `thiserror` for error types, `anyhow` for main error propagation

**Rationale**:
- `thiserror` provides clean derive macros for custom error types
- `anyhow` simplifies error handling in main.rs
- Matches Go's explicit error handling style conceptually

### Decision 5: Session State Management

**Choice**: Use `tokio::sync::RwLock<HashMap<K, V>>` for session maps

**Rationale**:
- Matches Go's `sync.RWMutex` pattern
- Allows concurrent reads, exclusive writes
- Familiar pattern for Go developers reading the code

**Alternative considered**:
- `dashmap`: Lock-free concurrent hashmap - more performant but less explicit, harder to reason about

### Decision 6: Graceful Shutdown

**Choice**: Use `tokio::signal` + `CancellationToken` pattern

**Rationale**:
- `tokio::signal::ctrl_c()` for SIGINT handling
- `tokio_util::sync::CancellationToken` for propagating shutdown
- Clean pattern matching Go's `context.WithCancel`

## Risks / Trade-offs

### Risk 1: Learning Curve
- **Risk**: Maintainers unfamiliar with Rust ownership model
- **Mitigation**: Keep code simple, prefer explicit patterns over clever abstractions

### Risk 2: Async Complexity
- **Risk**: Async Rust has known complexity (lifetimes with futures, Send bounds)
- **Mitigation**: Use `Arc` and owned data where possible, avoid complex lifetime situations

### Risk 3: Binary Size
- **Risk**: Rust binaries with Tokio can be larger than Go binaries
- **Mitigation**: Use `strip` and LTO in release builds; accept reasonable size trade-off

### Risk 4: Compile Time
- **Risk**: Rust compile times are longer than Go
- **Mitigation**: Use incremental compilation, split into library + binary

## Migration Plan

### Phase 1: Parallel Development
- Develop Rust implementation alongside Go version
- Both can coexist in the repository (different directories)

### Phase 2: Testing
- Run both implementations against the same integration tests
- Verify wire protocol compatibility

### Phase 3: Deployment
- Users can choose either binary
- Document any platform-specific differences

### Rollback
- Go version remains available if issues arise
- No database or state migration needed (stateless proxy)

## Open Questions

None - this is a straightforward rewrite with well-understood requirements.
