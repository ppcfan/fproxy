# fproxy

A UDP relay with redundant multi-path transmission and packet deduplication.

## Overview

fproxy enables reliable UDP forwarding through a client-server relay architecture. The client sends each UDP packet over multiple paths (UDP and/or TCP) simultaneously to the server, which deduplicates and forwards only unique packets to the target.

```
┌─────────────┐         ┌─────────────┐   UDP/TCP    ┌─────────────┐         ┌─────────────┐
│ UDP Source  │──UDP───▶│   Client    │═══════════▶│   Server    │──UDP───▶│   Target    │
│             │         │             │  multi-path  │             │         │             │
└─────────────┘         └─────────────┘              └─────────────┘         └─────────────┘
```

## Features

- **Multi-path transmission**: Send packets over multiple UDP and/or TCP paths simultaneously
- **Packet deduplication**: Server-side sliding window deduplication prevents duplicate delivery
- **Mixed protocols**: Support both UDP and TCP between client and server
- **Flexible configuration**: CLI flags and/or YAML config files
- **Graceful shutdown**: Clean handling of SIGINT/SIGTERM signals

## Installation

```bash
go build -o fproxy .
```

## Usage

### Client Mode

Listens for UDP packets and forwards them to server endpoints:

```bash
# CLI flags
./fproxy -mode client \
  -listen :5000 \
  -servers "server1:8001/udp,server1:8002/tcp"

# Config file
./fproxy -config client.yaml
```

### Server Mode

Receives packets from client, deduplicates, and forwards to target:

```bash
# CLI flags
./fproxy -mode server \
  -listen-addrs ":8001/udp,:8002/tcp" \
  -target "target-server:9000" \
  -dedup-window 10000

# Config file
./fproxy -config server.yaml
```

## Configuration

### CLI Flags

| Flag | Description |
|------|-------------|
| `-config` | Path to YAML config file |
| `-mode` | Mode: `client` or `server` |
| `-listen` | Client: UDP address to listen on (e.g., `:5000`) |
| `-servers` | Client: Server endpoints (e.g., `host:8001/udp,host:8002/tcp`) |
| `-listen-addrs` | Server: Listen addresses (e.g., `:8001/udp,:8002/tcp`) |
| `-target` | Server: Target UDP address (e.g., `target:9000`) |
| `-dedup-window` | Server: Deduplication window size (default: 10000) |

CLI flags take precedence over config file values.

### Config File Examples

**client.yaml**
```yaml
mode: client
client:
  listen_addr: ":5000"
  servers: "server.example.com:8001/udp,server.example.com:8002/tcp"
```

**server.yaml**
```yaml
mode: server
server:
  listen_addrs: ":8001/udp,:8002/tcp"
  target_addr: "target.example.com:9000"
  dedup_window: 10000
```

## Protocol

### Packet Format

```
┌──────────────────────┬─────────────────────────┐
│ Sequence Number      │ Payload                 │
│ (4 bytes, big-endian)│ (variable length)       │
└──────────────────────┴─────────────────────────┘
```

### TCP Framing

For TCP transport, packets are length-prefixed:

```
┌──────────────────────┬─────────────────────────┐
│ Length               │ Packet Data             │
│ (2 bytes, big-endian)│ (up to 65535 bytes)     │
└──────────────────────┴─────────────────────────┘
```

## Project Structure

```
fproxy/
├── main.go           # Entry point
├── config/
│   └── config.go     # Configuration loading and validation
├── protocol/
│   └── packet.go     # Packet encoding/decoding
├── client/
│   └── client.go     # Client mode implementation
├── server/
│   └── server.go     # Server mode implementation
└── examples/
    ├── client.yaml   # Example client config
    └── server.yaml   # Example server config
```

## Testing

```bash
go test ./...
```

## License

MIT
