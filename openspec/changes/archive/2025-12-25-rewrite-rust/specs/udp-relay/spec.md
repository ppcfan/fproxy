## MODIFIED Requirements

### Requirement: Client Mode
The system SHALL operate in client mode when configured. In client mode, the system SHALL listen on a specified UDP port, track sessions by source address, assign sequence numbers for deduplication, and forward packets to configured server endpoints. The client SHALL also receive response packets from servers and forward them back to the original source address. The implementation MAY be written in Go or Rust.

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
The system SHALL operate in server mode when configured. In server mode, the system SHALL listen on one or more ports (UDP and/or TCP), deduplicate received packets, create dedicated target UDP sockets per session, forward unique packets to the target server, and forward responses back to the originating client. The implementation MAY be written in Go or Rust.

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
