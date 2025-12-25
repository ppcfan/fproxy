# fproxy

A Layer 4 (L4) bidirectional UDP relay with redundant multi-path transmission and packet deduplication.

## Overview

fproxy enables reliable bidirectional UDP forwarding through a client-server relay architecture. The client sends each UDP packet over multiple paths (UDP and/or TCP) simultaneously to the server, which deduplicates and forwards only unique packets to the target. Responses from the target are relayed back to the original source.

```
┌─────────────┐         ┌─────────────┐   UDP/TCP    ┌─────────────┐         ┌─────────────┐
│ UDP Source  │◀──UDP──▶│   Client    │◀═══════════▶│   Server    │◀──UDP──▶│   Target    │
│             │         │             │  multi-path  │             │         │             │
└─────────────┘         └─────────────┘              └─────────────┘         └─────────────┘
```

## Features

- **Bidirectional relay**: Forwards both requests to target and responses back to source
- **Multi-path transmission**: Send packets over multiple UDP and/or TCP paths simultaneously
- **Per-session deduplication**: Server-side sliding window deduplication per session
- **Session management**: Each source UDP socket gets its own session with isolated state
- **Mixed protocols**: Support both UDP and TCP between client and server
- **Session timeout**: Automatic cleanup of inactive sessions
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
  -servers "192.168.1.100:8001/udp,192.168.1.100:8002/tcp" \
  -session-timeout 60s

# Config file
./fproxy -config client.yaml
```

### Server Mode

Receives packets from client, deduplicates, and forwards to target:

```bash
# CLI flags
./fproxy -mode server \
  -listen-addrs ":8001/udp,:8002/tcp" \
  -target "192.168.1.200:9000" \
  -dedup-window 10000 \
  -session-timeout 60s

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
| `-servers` | Client: Server endpoints (e.g., `192.168.1.100:8001/udp,192.168.1.100:8002/tcp`) |
| `-listen-addrs` | Server: Listen addresses (e.g., `:8001/udp,:8002/tcp`) |
| `-target` | Server: Target UDP address (e.g., `192.168.1.200:9000`) |
| `-dedup-window` | Server: Deduplication window size (default: 10000) |
| `-session-timeout` | Session timeout duration (default: 60s) |
| `-verbose` | Enable debug-level logging |

CLI flags take precedence over config file values.

### Configuration Constraints

**Client server endpoints must use IP addresses:**

- Domain names are not supported for server endpoints
- All server endpoints must point to the same IP address (only port and protocol may differ)
- This ensures deterministic behavior and proper deduplication across paths

```bash
# Valid: same IP, different ports/protocols
-servers "192.168.1.100:8001/udp,192.168.1.100:8002/tcp"

# Valid: IPv6 address
-servers "[2001:db8::1]:8001/udp,[2001:db8::1]:8002/tcp"

# Invalid: domain name (will be rejected)
-servers "server.example.com:8001/udp"

# Invalid: different IPs (will be rejected)
-servers "192.168.1.100:8001/udp,192.168.1.101:8002/tcp"
```

### Config File Examples

**client.yaml**
```yaml
mode: client
client:
  listen_addr: ":5000"
  servers: "192.168.1.100:8001/udp,192.168.1.100:8002/tcp"
  session_timeout: 60s
```

**server.yaml**
```yaml
mode: server
server:
  listen_addrs: ":8001/udp,:8002/tcp"
  target_addr: "192.168.1.200:9000"
  dedup_window: 10000
  session_timeout: 60s
```

## Protocol

### Session Management

Each source UDP address:port combination is assigned a unique session. Sessions are tracked independently for:
- **Request deduplication**: Each session has its own deduplication window
- **Response routing**: Responses from target are routed back to the correct source

### Packet Format

**Both UDP and TCP Transport** (between client and server):
```
┌──────────────────────┬──────────────────────┬─────────────────────────┐
│ Session ID           │ Sequence Number      │ Payload                 │
│ (4 bytes, big-endian)│ (4 bytes, big-endian)│ (variable length)       │
└──────────────────────┴──────────────────────┴─────────────────────────┘
```

UDP and TCP use the same packet format for unified session tracking. This enables:
- Cross-path deduplication (same session across UDP and TCP)
- Multi-path response redundancy (responses sent via all paths)

### TCP Framing

For TCP transport, packets are length-prefixed:

```
┌──────────────────────┬─────────────────────────┐
│ Length               │ Packet Data             │
│ (2 bytes, big-endian)│ (up to 65535 bytes)     │
└──────────────────────┴─────────────────────────┘
```

## Architecture

### Client

1. Listens for UDP packets from sources
2. Creates a session for each unique source address
3. Assigns session IDs and sequence numbers
4. Forwards packets to all configured server endpoints (multi-path)
5. Receives responses from all paths
6. Deduplicates responses and forwards unique ones to the original source

### Server

1. Listens on multiple UDP/TCP endpoints
2. Creates a unified session per sessionID (shared across UDP and TCP)
3. Deduplicates packets per session using sequence numbers
4. Forwards unique packets to the target
5. Receives responses from target
6. Sends responses via all active paths for the session (multi-path)

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
