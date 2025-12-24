# Change: Add UDP Relay with Redundant Multi-Path Transmission

## Why
UDP traffic is unreliable by nature. This feature enables reliable UDP forwarding by using a client-server relay architecture with packet deduplication and redundant multi-path transmission. The client can send packets over multiple paths (UDP and/or TCP) simultaneously to the server, which deduplicates and forwards only unique packets to the target.

## What Changes
- Add **client mode**: listens on a local UDP port, numbers packets, and forwards to server endpoints
- Add **server mode**: listens on multiple ports (UDP/TCP), deduplicates packets by sequence number, forwards to target UDP server
- Add **configuration system**: CLI flags and config file support for mode, listen addresses, and target addresses
- Add **packet protocol**: simple framing with sequence numbers for deduplication

## Impact
- Affected specs: `udp-relay` (new capability)
- Affected code: New packages for relay client, relay server, configuration, and packet protocol
