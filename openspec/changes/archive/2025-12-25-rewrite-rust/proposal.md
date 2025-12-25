# Change: Rewrite fproxy in Rust

## Why

The project currently uses Go for a Layer 4 UDP/TCP relay implementation. A Rust rewrite offers potential benefits:
- **Memory safety without garbage collection**: Predictable latency for proxy workloads
- **Zero-cost abstractions**: High performance with no runtime overhead
- **Strong type system**: Catch more bugs at compile time
- **Async/await with Tokio**: Mature async runtime for network applications
- **Single binary deployment**: Similar to Go, but with smaller binary sizes

## What Changes

- **BREAKING**: Complete rewrite of the codebase from Go to Rust
- New project structure following Rust conventions
- Replace Go standard library networking with Tokio async runtime
- Replace `slog` logging with `tracing` crate
- Replace `gopkg.in/yaml.v3` with `serde` + `serde_yaml` for configuration
- Replace Go's `flag` package with `clap` for CLI argument parsing
- Maintain 100% functional parity with the existing Go implementation

## Impact

- Affected specs: `udp-relay` (implementation language change, behavior unchanged)
- Affected code: All source files will be replaced
- External dependencies: Shift from Go modules to Cargo/crates.io ecosystem
- Build system: Replace `go build` with `cargo build`
- Testing: Replace Go testing with Rust's built-in test framework

## Migration Notes

- Existing Go binaries will continue to work until replaced
- Configuration file format (YAML) remains compatible
- CLI flags remain the same
- Protocol wire format is unchanged (big-endian, same header sizes)
- Users can switch between Go and Rust binaries without reconfiguration

## Non-Goals

- Adding new features (this is a pure rewrite)
- Changing the protocol or configuration format
- Changing observable behavior
