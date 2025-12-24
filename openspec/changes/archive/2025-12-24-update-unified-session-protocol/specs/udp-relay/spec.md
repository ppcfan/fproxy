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
- **AND** uses the same session_id as the UDP transport for unified session tracking
- **AND** assigns the next sequence number for that session
- **AND** sends the packet with session_id and seq over the TCP connection

#### Scenario: Client sends to multiple server endpoints
- **WHEN** multiple server endpoints are configured (e.g., server:8001/udp, server:8002/tcp)
- **THEN** the client sends each packet to all endpoints using unified packet format

#### Scenario: Client receives and forwards response (UDP transport)
- **WHEN** client receives a response packet from a UDP server endpoint
- **THEN** the client extracts the session_id from the packet
- **AND** looks up the source address for that session
- **AND** forwards the response payload to the original source address

#### Scenario: Client receives and forwards response (TCP transport)
- **WHEN** client receives a response packet from a TCP server connection
- **THEN** the client extracts the session_id from the packet
- **AND** looks up the source address for that session
- **AND** forwards the response payload to the original source address
- **AND** updates the session activity timestamp

#### Scenario: Client discards response with unknown session
- **WHEN** client receives a response with an unknown session_id
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
- **AND** the (session_id, seq) pair has not been seen before
- **THEN** the server extracts the session_id from the packet
- **AND** looks up or creates a dedicated target UDP socket for that session
- **AND** forwards the packet payload to the target server via that socket
- **AND** marks the (session_id, seq) pair as seen

#### Scenario: Server discards duplicate packet (UDP transport)
- **WHEN** server receives a packet via UDP with a (session_id, seq) pair already seen
- **THEN** the server discards the packet without forwarding

#### Scenario: Server discards duplicate packet (TCP transport)
- **WHEN** server receives a packet via TCP with a (session_id, seq) pair already seen
- **THEN** the server discards the packet without forwarding

#### Scenario: Server listens on multiple ports and protocols
- **WHEN** multiple listen addresses are configured (e.g., :8001/udp, :8002/tcp)
- **THEN** the server accepts connections on all configured addresses

#### Scenario: Server receives and forwards response (UDP transport)
- **WHEN** server receives a response from a target socket
- **THEN** the server looks up the session_id for that target socket
- **AND** encodes the response with the session_id and sequence number
- **AND** forwards the response to the client endpoint via all registered paths

#### Scenario: Server receives and forwards response (TCP transport)
- **WHEN** server receives a response from a target socket associated with a session
- **THEN** the server encodes the response with the session_id and sequence number
- **AND** forwards the response over all registered TCP connections for that session
- **AND** uses a write loop to handle short writes

### Requirement: Packet Protocol
The system SHALL use a unified packet format for both UDP and TCP transport between client and server. All multi-byte integer fields SHALL use big-endian byte order.

#### Scenario: UDP transport packet format
- **WHEN** a packet is transmitted via UDP transport (request or response)
- **THEN** the protocol layer format is `[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]`
- **AND** the total protocol header size is 8 bytes

#### Scenario: TCP transport packet format
- **WHEN** a packet is transmitted via TCP transport (request or response)
- **THEN** the protocol layer format is `[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]`
- **AND** the total protocol header size is 8 bytes
- **AND** the TCP sender/receiver adds a 2-byte length prefix (big-endian) for framing

#### Scenario: Packet decoding
- **WHEN** a packet is received
- **THEN** the system parses fields according to the unified format
- **AND** extracts session_id, sequence number, and payload

### Requirement: Deduplication Window
The server SHALL maintain deduplication state per session to detect duplicate packets. The window size SHALL be configurable with a default of 10000.

#### Scenario: UDP transport deduplication
- **WHEN** server receives packets via UDP transport
- **THEN** deduplication is performed per session_id
- **AND** each session maintains its own sliding window of seen sequence numbers
- **AND** packets with (session_id, seq) pairs already seen are discarded

#### Scenario: TCP transport deduplication
- **WHEN** server receives packets via TCP transport
- **THEN** deduplication is performed per session_id (not per TCP connection)
- **AND** packets from different TCP connections with the same session_id share deduplication state
- **AND** packets with (session_id, seq) pairs already seen are discarded

#### Scenario: Cross-path deduplication
- **WHEN** server receives packets via both UDP and TCP transport for the same session_id
- **THEN** deduplication is unified across transport types
- **AND** duplicate packets are discarded regardless of which transport path they arrive on

#### Scenario: Session cleanup removes dedup state
- **WHEN** a session times out or is closed
- **THEN** the associated deduplication window is also removed

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
- **THEN** the client reuses the session_id from the UDP session for that source
- **AND** creates a new TCP connection to the server for that source
- **AND** stores the mapping sourceAddr -> (session_id, tcpConn, seqCounter)
- **AND** starts a response receiver goroutine for that connection

#### Scenario: Client session activity update
- **WHEN** client sends or receives data via any transport path (UDP or TCP)
- **THEN** the session activity timestamp is updated for both UDP and TCP session tracking
- **AND** session cleanup considers activity from all transport paths

#### Scenario: Server session creation (UDP transport)
- **WHEN** server receives a packet with a new session_id via UDP
- **THEN** the server creates a new UDP socket connected to the target
- **AND** creates a new deduplication window for that session
- **AND** stores the mapping session_id -> (targetSocket, dedupWindow)
- **AND** starts a response receiver goroutine for that socket

#### Scenario: Server session creation (TCP transport)
- **WHEN** server receives a packet with a session_id via TCP
- **THEN** the server looks up or creates a target socket for that session_id
- **AND** registers the TCP connection as a path for that session
- **AND** shares deduplication state with any UDP paths for the same session

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
- **AND** encodes and sends the response to all registered client paths (UDP and TCP)

#### Scenario: Client reads server response
- **WHEN** a sender connection receives response data from the server
- **THEN** the client reads and decodes the response
- **AND** extracts the session_id to look up the source address
- **AND** sends the response payload to the source via listener.WriteToUDP()

#### Scenario: One source socket equals one target socket
- **WHEN** the system is handling traffic from multiple source addresses
- **THEN** each source address has exactly one corresponding target socket on the server
- **AND** responses from each target socket are routed only to the corresponding source
- **AND** responses may be sent via multiple transport paths for redundancy
