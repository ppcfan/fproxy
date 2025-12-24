# Tasks: Add Bidirectional UDP Relay

## 1. Protocol Layer

- [x] 1.1 Add `EncodeUDP(sessionID, seq, payload)` for UDP transport: `[4B session_id][4B seq][payload]`, all big-endian
- [x] 1.2 Add `DecodeUDP(packet)` returning `(sessionID, seq, payload, error)`
- [x] 1.3 Add constants: `UDPHeaderSize = 8` (session_id + seq)
- [x] 1.4 Rename existing `Encode/Decode` to `EncodeTCP/DecodeTCP` for clarity (format unchanged: `[4B seq][payload]`)
- [x] 1.5 Add constant: `TCPHeaderSize = 4` (seq only, length prefix is handled by TCP sender/receiver)
- [x] 1.6 Add unit tests for UDP protocol functions (verify big-endian encoding)

## 2. Configuration

- [x] 2.1 Add `SessionTimeout` field to `ClientConfig` (default: 60s)
- [x] 2.2 Add `SessionTimeout` field to `ServerConfig` (default: 60s)
- [x] 2.3 Update YAML parsing for `session_timeout`
- [x] 2.4 Add `-session-timeout` CLI flag

## 3. Per-Session Deduplication

- [x] 3.1 Modify `DedupWindow` to be instantiable per session (remove global state)
- [x] 3.2 Server UDP: maintain `map[sessionID]*DedupWindow`
- [x] 3.3 Server TCP: maintain `map[tcpConn]*DedupWindow`
- [x] 3.4 Remove dedup window when session is cleaned up
- [x] 3.5 Add unit tests for per-session deduplication

## 4. Server Changes

- [x] 4.1 Define `TargetSession` struct: `{targetConn, dedupWindow, lastActivity, cancel}`
- [x] 4.2 Add `udpSessions` map: `map[uint32]*TargetSession` (sessionID -> target session)
- [x] 4.3 Add `tcpSessions` map: `map[net.Conn]*TargetSession` (tcpConn -> target session)
- [x] 4.4 Modify UDP `handlePacket()`: decode sessionID, get/create target session, dedup per session
- [x] 4.5 Modify `handleTCPConn()`: create target session per TCP connection, dedup per connection
- [x] 4.6 Implement `handleTargetResponse()` goroutine per target session
- [x] 4.7 UDP response: encode with sessionID, send to client via UDP listener
- [x] 4.8 TCP response: encode without sessionID, send over TCP connection (with length prefix)
- [x] 4.9 Implement periodic session cleanup (check lastActivity vs timeout)
- [x] 4.10 Update `Stop()` to close all target sessions and clear maps

## 5. Client Changes

- [x] 5.1 Define `ClientSession` struct: `{sessionID, seq, lastActivity}` for UDP, `{tcpConn, seq, lastActivity}` for TCP
- [x] 5.2 Add `udpSessions` map: `map[string]*ClientSession` (sourceAddr -> session) for UDP transport
- [x] 5.3 Add `tcpSessions` map: `map[string]*TCPClientSession` (sourceAddr -> TCP session) for TCP transport
- [x] 5.4 Add `sessionIDCounter` atomic.Uint32 for generating session IDs
- [x] 5.5 Modify `handlePacket()`: capture source address, get/create session, use per-session seq
- [x] 5.6 UDP transport: encode with sessionID using `EncodeUDP()`
- [x] 5.7 TCP transport: create separate TCP connection per source, encode with `EncodeTCP()`
- [x] 5.8 Add `handleUDPResponse()` goroutine: read from UDP sender, decode sessionID, forward to source
- [x] 5.9 Add response reader per TCP connection: read frames, decode, forward to source
- [x] 5.10 Implement `forwardResponse()`: lookup sourceAddr, `listener.WriteToUDP()`
- [x] 5.11 Implement periodic session cleanup
- [x] 5.12 Update `Stop()` to close all sessions

## 6. Integration Testing

- [x] 6.1 Test: single source bidirectional via UDP transport
- [x] 6.2 Test: single source bidirectional via TCP transport
- [x] 6.3 Test: multiple sources concurrent via UDP transport (verify session isolation)
- [x] 6.4 Test: multiple sources concurrent via TCP transport
- [x] 6.5 Test: deduplication works per session (same seq from different sessions both accepted)
- [x] 6.6 Test: session timeout closes target socket
- [x] 6.7 Test: response to unknown session is discarded
- [x] 6.8 Update existing tests for new packet format

## 7. Documentation

- [x] 7.1 Update README with bidirectional usage examples
- [x] 7.2 Document session timeout configuration
- [x] 7.3 Document packet format (UDP vs TCP transport)
