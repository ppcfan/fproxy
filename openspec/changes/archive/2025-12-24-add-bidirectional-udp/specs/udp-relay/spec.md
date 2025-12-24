## MODIFIED Requirements

### Requirement: Client Mode
The system SHALL operate in client mode when configured. In client mode, the system SHALL listen on a specified UDP port, track sessions by source address, assign sequence numbers for deduplication, and forward packets to configured server endpoints. The client SHALL also receive response packets from servers and forward them back to the original source address.

#### Scenario: Client receives and forwards UDP packet (UDP transport)
- **WHEN** client mode is active with UDP transport and a UDP packet is received from a source address
- **THEN** the client looks up or creates a session for the source address
- **AND** assigns the next sequence number for that session
- **AND** sends the packet with session_id and seq to the server endpoint

#### Scenario: Client receives and forwards UDP packet (TCP transport)
- **WHEN** client mode is active with TCP transport and a UDP packet is received from a source address
- **THEN** the client looks up or creates a dedicated TCP connection for the source address
- **AND** assigns the next sequence number for that session
- **AND** sends the packet with seq (no session_id) over the TCP connection

#### Scenario: Client sends to multiple server endpoints
- **WHEN** multiple server endpoints are configured (e.g., server:8001/udp, server:8002/tcp)
- **THEN** the client sends each packet to all endpoints using appropriate format per transport

#### Scenario: Client receives and forwards response (UDP transport)
- **WHEN** client receives a response packet from a UDP server endpoint
- **THEN** the client extracts the session_id from the packet
- **AND** looks up the source address for that session
- **AND** forwards the response payload to the original source address

#### Scenario: Client receives and forwards response (TCP transport)
- **WHEN** client receives a response packet from a TCP server connection
- **THEN** the client looks up the source address associated with that TCP connection
- **AND** forwards the response payload to the original source address

#### Scenario: Client discards response with unknown session
- **WHEN** client receives a response with an unknown session_id or from an unknown TCP connection
- **THEN** the client discards the response and logs a warning

### Requirement: Server Mode
The system SHALL operate in server mode when configured. In server mode, the system SHALL listen on one or more ports (UDP and/or TCP), deduplicate received packets, create dedicated target UDP sockets per session, forward unique packets to the target server, and forward responses back to the originating client.

#### Scenario: Server receives and forwards unique packet (UDP transport)
- **WHEN** server receives a request packet via UDP listener
- **AND** the (session_id, seq) pair has not been seen before
- **THEN** the server extracts the session_id
- **AND** looks up or creates a dedicated target UDP socket for that session
- **AND** forwards the packet payload to the target server via that socket
- **AND** marks the (session_id, seq) pair as seen

#### Scenario: Server receives and forwards unique packet (TCP transport)
- **WHEN** server receives a request packet via TCP connection
- **AND** the seq has not been seen for this TCP connection
- **THEN** the server looks up or creates a dedicated target UDP socket for that TCP connection
- **AND** forwards the packet payload to the target server via that socket
- **AND** marks the seq as seen for this TCP connection

#### Scenario: Server discards duplicate packet (UDP transport)
- **WHEN** server receives a packet via UDP with a (session_id, seq) pair already seen
- **THEN** the server discards the packet without forwarding

#### Scenario: Server discards duplicate packet (TCP transport)
- **WHEN** server receives a packet via TCP with a seq already seen for that connection
- **THEN** the server discards the packet without forwarding

#### Scenario: Server listens on multiple ports and protocols
- **WHEN** multiple listen addresses are configured (e.g., :8001/udp, :8002/tcp)
- **THEN** the server accepts connections on all configured addresses

#### Scenario: Server receives and forwards response (UDP transport)
- **WHEN** server receives a response from a target socket
- **THEN** the server looks up the session_id for that target socket
- **AND** encodes the response with the session_id
- **AND** forwards the response to the client endpoint

#### Scenario: Server receives and forwards response (TCP transport)
- **WHEN** server receives a response from a target socket associated with a TCP connection
- **THEN** the server encodes the response (without session_id)
- **AND** forwards the response over the TCP connection to the client

### Requirement: Packet Protocol
The system SHALL use different packet formats depending on the transport between client and server. All multi-byte integer fields SHALL use big-endian byte order.

#### Scenario: UDP transport packet format
- **WHEN** a packet is transmitted via UDP transport (request or response)
- **THEN** the protocol layer format is `[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]`
- **AND** the total protocol header size is 8 bytes

#### Scenario: TCP transport packet format
- **WHEN** a packet is transmitted via TCP transport (request or response)
- **THEN** the protocol layer format is `[4-byte seq (big-endian)][payload]`
- **AND** the total protocol header size is 4 bytes
- **AND** the TCP sender/receiver adds a 2-byte length prefix (big-endian) for framing

#### Scenario: Packet decoding
- **WHEN** a packet is received
- **THEN** the system parses fields according to the transport type
- **AND** extracts session_id (UDP) or uses connection identity (TCP)
- **AND** extracts the sequence number and payload

### Requirement: Deduplication Window
The server SHALL maintain deduplication state per session to detect duplicate packets. The window size SHALL be configurable with a default of 10000.

#### Scenario: UDP transport deduplication
- **WHEN** server receives packets via UDP transport
- **THEN** deduplication is performed per session_id
- **AND** each session maintains its own sliding window of seen sequence numbers
- **AND** packets with (session_id, seq) pairs already seen are discarded

#### Scenario: TCP transport deduplication
- **WHEN** server receives packets via TCP transport
- **THEN** deduplication is performed per TCP connection
- **AND** each TCP connection maintains its own sliding window of seen sequence numbers
- **AND** packets with seq already seen on that connection are discarded

#### Scenario: Session cleanup removes dedup state
- **WHEN** a session times out or is closed
- **THEN** the associated deduplication window is also removed

### Requirement: Configuration
The system SHALL support configuration via command-line flags and/or a YAML configuration file. CLI flags SHALL take precedence over config file values.

#### Scenario: Client mode configuration
- **WHEN** running in client mode
- **THEN** the following MUST be configurable:
  - Mode: `client`
  - Listen address: UDP address to listen on (e.g., `:5000`)
  - Server endpoints: List of server addresses with protocol (e.g., `server:8001/udp,server:8002/tcp`)
  - Session timeout: Duration before inactive sessions expire (default: 60s)
  - Verbose: Enable debug-level logging (optional, default: false)

#### Scenario: Server mode configuration
- **WHEN** running in server mode
- **THEN** the following MUST be configurable:
  - Mode: `server`
  - Listen addresses: List of addresses with protocol to listen on (e.g., `:8001/udp,:8002/tcp`)
  - Target address: UDP address of the target server (e.g., `target:9000`)
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

## ADDED Requirements

### Requirement: Session Management
The system SHALL maintain session state to map source UDP addresses to target UDP sockets for bidirectional communication.

#### Scenario: Client session creation (UDP transport)
- **WHEN** client receives a UDP packet from a new source address and uses UDP transport
- **THEN** the client generates a new session_id for that source address
- **AND** initializes a sequence counter for that session starting at 0
- **AND** stores the mapping sourceAddr -> (session_id, seqCounter)
- **AND** updates the last activity timestamp

#### Scenario: Client session creation (TCP transport)
- **WHEN** client receives a UDP packet from a new source address and uses TCP transport
- **THEN** the client creates a new TCP connection to the server for that source
- **AND** initializes a sequence counter for that session starting at 0
- **AND** stores the mapping sourceAddr -> (tcpConn, seqCounter)
- **AND** starts a response receiver goroutine for that connection

#### Scenario: Server session creation (UDP transport)
- **WHEN** server receives a packet with a new session_id via UDP
- **THEN** the server creates a new UDP socket connected to the target
- **AND** creates a new deduplication window for that session
- **AND** stores the mapping session_id -> (targetSocket, dedupWindow)
- **AND** starts a response receiver goroutine for that socket

#### Scenario: Server session creation (TCP transport)
- **WHEN** server accepts a new TCP connection from client
- **THEN** the server creates a new UDP socket connected to the target
- **AND** creates a new deduplication window for that connection
- **AND** stores the mapping tcpConn -> (targetSocket, dedupWindow)
- **AND** starts a response receiver goroutine for that socket

#### Scenario: Session timeout and cleanup
- **WHEN** a session has no activity for longer than the configured timeout
- **THEN** the system closes the associated socket (target socket on server, TCP conn on client)
- **AND** removes the session from all maps including deduplication state
- **AND** logs the session cleanup

#### Scenario: Session cleanup on shutdown
- **WHEN** the system receives a shutdown signal
- **THEN** all sessions and associated sockets are closed
- **AND** all maps are cleared

### Requirement: Response Routing
The system SHALL route responses from the target server back to the original source UDP address.

#### Scenario: Server reads target response
- **WHEN** a target socket receives data from the target server
- **THEN** the server reads the response data
- **AND** looks up the session (by socket) to determine routing
- **AND** encodes and sends the response to the client

#### Scenario: Client reads server response
- **WHEN** a sender connection receives response data from the server
- **THEN** the client reads and decodes the response
- **AND** looks up the source address for the session
- **AND** sends the response payload to the source via listener.WriteToUDP()

#### Scenario: One source socket equals one target socket
- **WHEN** the system is handling traffic from multiple source addresses
- **THEN** each source address has exactly one corresponding target socket on the server
- **AND** responses from each target socket are routed only to the corresponding source
