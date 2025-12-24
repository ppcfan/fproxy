## 1. Core Protocol
- [x] 1.1 Create `protocol/packet.go` with Encode/Decode functions for sequence number + payload
- [x] 1.2 Write unit tests for packet encoding/decoding

## 2. Configuration
- [x] 2.1 Create `config/config.go` with struct definitions for client/server config
- [x] 2.2 Implement YAML config file parsing
- [x] 2.3 Implement CLI flag parsing with flag package
- [x] 2.4 Add config validation (required fields, valid addresses)
- [x] 2.5 Write unit tests for config loading and validation

## 3. Server Implementation
- [x] 3.1 Create `server/server.go` with Server struct
- [x] 3.2 Implement multi-protocol listener (UDP and TCP)
- [x] 3.3 Implement deduplication window with sliding window logic
- [x] 3.4 Implement packet forwarding to target UDP server
- [x] 3.5 Add graceful shutdown handling
- [x] 3.6 Write unit tests for deduplication logic
- [x] 3.7 Write integration tests for server forwarding

## 4. Client Implementation
- [x] 4.1 Create `client/client.go` with Client struct
- [x] 4.2 Implement UDP listener for incoming packets
- [x] 4.3 Implement sequence number assignment
- [x] 4.4 Implement multi-endpoint sender (UDP and TCP connections)
- [x] 4.5 Add graceful shutdown handling
- [x] 4.6 Write unit tests for sequence numbering
- [x] 4.7 Write integration tests for client forwarding

## 5. Main Entry Point
- [x] 5.1 Update `main.go` to parse mode and dispatch to client/server
- [x] 5.2 Add signal handling for graceful shutdown
- [x] 5.3 Add structured logging setup

## 6. Documentation and Testing
- [x] 6.1 Add example config files (client.yaml, server.yaml)
- [x] 6.2 Run full end-to-end test: source → client → server → target
- [x] 6.3 Verify deduplication works with multi-path transmission
