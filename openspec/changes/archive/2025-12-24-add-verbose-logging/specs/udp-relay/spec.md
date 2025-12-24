## MODIFIED Requirements

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
