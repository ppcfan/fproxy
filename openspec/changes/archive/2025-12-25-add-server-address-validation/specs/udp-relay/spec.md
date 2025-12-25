## MODIFIED Requirements

### Requirement: Configuration
The system SHALL support configuration via command-line flags and/or a YAML configuration file. CLI flags SHALL take precedence over config file values.

#### Scenario: Client mode configuration
- **WHEN** running in client mode
- **THEN** the following MUST be configurable:
  - Mode: `client`
  - Listen address: UDP address to listen on (e.g., `:5000`)
  - Server endpoints: List of server addresses with protocol (e.g., `192.168.1.1:8001/udp,192.168.1.1:8002/tcp`)
  - Session timeout: Duration before inactive sessions expire (default: 60s)
  - Verbose: Enable debug-level logging (optional, default: false)

#### Scenario: Client server endpoint IP address requirement
- **WHEN** client mode configuration specifies server endpoints
- **THEN** all server endpoint addresses MUST use IP addresses (IPv4 or IPv6), not domain names
- **AND** if a domain name is used, the system SHALL reject the configuration with an error message indicating that only IP addresses are supported

#### Scenario: Client server endpoint same-IP validation
- **WHEN** client mode configuration contains multiple server endpoints
- **THEN** all endpoints MUST have the same IP address (only port and protocol may differ)
- **AND** if endpoints have different IP addresses, the system SHALL reject the configuration with an error message indicating that different server IPs are not supported because deduplication cannot work across separate servers

#### Scenario: Server mode configuration
- **WHEN** running in server mode
- **THEN** the following MUST be configurable:
  - Mode: `server`
  - Listen addresses: List of addresses with protocol to listen on (e.g., `:8001/udp,:8002/tcp`)
  - Target address: UDP address of the target server (e.g., `192.168.1.100:9000`)
  - Dedup window size: Number of sequence numbers to track per session (default: 10000)
  - Session timeout: Duration before target sockets are closed (default: 60s)
  - Verbose: Enable debug-level logging (optional, default: false)

#### Scenario: Config file usage
- **WHEN** `--config` flag specifies a YAML file path
- **THEN** the system loads configuration from that file
- **AND** CLI flags override any values from the file

#### Scenario: Verbose flag usage
- **WHEN** `-verbose` flag is provided or `verbose: true` is set in config file
- **THEN** the system enables debug-level logging
- **AND** packet forwarding details (session_id, sequence number, size, direction) are logged
