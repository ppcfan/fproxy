# Project Context

## Purpose
fproxy is a Layer 4 (L4) forward proxy that forwards TCP and UDP traffic to destination servers. It operates at the transport layer, enabling transparent proxying of network connections without inspecting application-layer protocols.

## Tech Stack
- Go 1.25.5
- Standard library for networking (`net` package)

## Project Conventions

### Code Style
- Follow standard Go idioms and conventions
- Use `gofmt` for formatting
- Adhere to [Effective Go](https://go.dev/doc/effective_go) guidelines
- Use meaningful variable/function names following Go naming conventions (camelCase for unexported, PascalCase for exported)
- Keep functions focused and small

### Architecture Patterns
- Keep the codebase simple and modular
- Separate concerns: connection handling, logging, configuration
- Use interfaces for testability and flexibility
- Prefer composition over inheritance

### Testing Strategy
- Use Go's built-in `testing` package
- Table-driven tests where appropriate
- Test files alongside source files (`*_test.go`)
- Aim for high coverage on core proxy logic

### Git Workflow
- Conventional commits recommended (feat:, fix:, docs:, etc.)
- Feature branches for new work
- Keep commits atomic and focused

## Domain Context
- L4 proxy operates at the transport layer (TCP/UDP)
- Does not inspect or modify application-layer data (HTTP, TLS, etc.)
- Must handle connection lifecycle: accept, forward, close
- Performance and low latency are important for proxy workloads

## Important Constraints
- Must handle concurrent connections efficiently
- Proper resource cleanup (connection closing, goroutine management)
- Graceful shutdown support

## External Dependencies
- Structured logging/metrics (to be added)
- No external dependencies currently - using Go standard library
