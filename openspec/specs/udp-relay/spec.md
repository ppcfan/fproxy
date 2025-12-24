# udp-relay Specification

## Purpose
TBD - created by archiving change add-udp-relay. Update Purpose after archive.
## Requirements
### Requirement: Client Mode
The system SHALL operate in client mode when configured. In client mode, the system SHALL listen on a specified UDP port, assign a sequence number to each received packet, and forward the numbered packet to all configured server endpoints.

#### Scenario: Client receives and forwards UDP packet
- **WHEN** client mode is active and a UDP packet is received on the listen port
- **THEN** the client assigns the next sequence number to the packet
- **AND** sends the numbered packet to all configured server endpoints (UDP and/or TCP)

#### Scenario: Client sends to multiple server endpoints
- **WHEN** multiple server endpoints are configured (e.g., server:8001/udp, server:8002/tcp)
- **THEN** the client sends each numbered packet to all endpoints simultaneously

### Requirement: Server Mode
The system SHALL operate in server mode when configured. In server mode, the system SHALL listen on one or more ports (UDP and/or TCP), deduplicate received packets by sequence number, and forward unique packets to the configured target UDP server.

#### Scenario: Server receives and forwards unique packet
- **WHEN** server mode is active and a numbered packet is received
- **AND** the sequence number has not been seen before
- **THEN** the server forwards the packet payload to the target UDP server
- **AND** marks the sequence number as seen

#### Scenario: Server discards duplicate packet
- **WHEN** server mode is active and a numbered packet is received
- **AND** the sequence number has already been seen
- **THEN** the server discards the packet without forwarding

#### Scenario: Server listens on multiple ports and protocols
- **WHEN** multiple listen addresses are configured (e.g., :8001/udp, :8002/tcp)
- **THEN** the server accepts connections on all configured addresses

### Requirement: Packet Protocol
The system SHALL use a binary packet format consisting of a 4-byte big-endian sequence number followed by the original UDP payload.

#### Scenario: Packet encoding
- **WHEN** a packet is encoded for transmission
- **THEN** the format is `[4-byte sequence number (big-endian)][payload bytes]`

#### Scenario: Packet decoding
- **WHEN** a packet is received
- **THEN** the first 4 bytes are parsed as the sequence number
- **AND** the remaining bytes are the payload

### Requirement: Deduplication Window
The server SHALL maintain a sliding window of seen sequence numbers to detect duplicates. The window size SHALL be configurable with a default of 10000.

#### Scenario: Sequence number within window
- **WHEN** a packet arrives with a sequence number within the current window range
- **THEN** the server checks if it has been seen and processes accordingly

#### Scenario: Sequence number beyond window
- **WHEN** a packet arrives with a sequence number beyond the current window
- **THEN** the window slides forward and old sequence numbers are forgotten

### Requirement: Configuration
The system SHALL support configuration via command-line flags and/or a YAML configuration file. CLI flags SHALL take precedence over config file values.

#### Scenario: Client mode configuration
- **WHEN** running in client mode
- **THEN** the following MUST be configurable:
  - Mode: `client`
  - Listen address: UDP address to listen on (e.g., `:5000`)
  - Server endpoints: List of server addresses with protocol (e.g., `server:8001/udp,server:8002/tcp`)
  - Verbose: Enable debug-level logging (optional, default: false)

#### Scenario: Server mode configuration
- **WHEN** running in server mode
- **THEN** the following MUST be configurable:
  - Mode: `server`
  - Listen addresses: List of addresses with protocol to listen on (e.g., `:8001/udp,:8002/tcp`)
  - Target address: UDP address of the target server (e.g., `target:9000`)
  - Dedup window size: Number of sequence numbers to track (default: 10000)
  - Verbose: Enable debug-level logging (optional, default: false)

#### Scenario: Config file usage
- **WHEN** `--config` flag specifies a YAML file path
- **THEN** the system loads configuration from that file
- **AND** CLI flags override any values from the file

#### Scenario: Verbose flag usage
- **WHEN** `-verbose` flag is provided or `verbose: true` is set in config file
- **THEN** the system enables debug-level logging
- **AND** packet forwarding details (sequence number, size) are logged

### Requirement: Logging
The system SHALL log significant events including startup, shutdown, errors, and optionally packet forwarding statistics.

#### Scenario: Startup logging
- **WHEN** the system starts
- **THEN** it logs the mode, listen addresses, and target/server endpoints

#### Scenario: Error logging
- **WHEN** an error occurs (e.g., failed to bind port, failed to send packet)
- **THEN** the error is logged with context

#### Scenario: Verbose packet logging
- **WHEN** verbose mode is enabled
- **THEN** the system logs each packet forwarding event with sequence number and payload size
- **AND** duplicate packet discards are logged with sequence number

### Requirement: Graceful Shutdown
The system SHALL handle SIGINT and SIGTERM signals and perform a graceful shutdown, closing all connections and listeners.

#### Scenario: Graceful shutdown on signal
- **WHEN** SIGINT or SIGTERM is received
- **THEN** the system stops accepting new packets
- **AND** closes all active connections and listeners
- **AND** exits cleanly

