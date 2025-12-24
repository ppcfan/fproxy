package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	// TCPHeaderSize is the protocol header size for TCP transport: [4-byte session_id][4-byte seq]
	// Note: TCP framing (2-byte length prefix) is handled by the sender/receiver, not here.
	TCPHeaderSize = 8

	// UDPHeaderSize is the protocol header size for UDP transport: [4-byte session_id][4-byte seq]
	UDPHeaderSize = 8
)

var (
	ErrPacketTooShort    = errors.New("packet too short: must be at least 8 bytes")
	ErrUDPPacketTooShort = errors.New("UDP packet too short: must be at least 8 bytes")
)

// EncodeTCP creates a packet for TCP transport with session ID, sequence number, and payload.
// Format: [4-byte session_id (big-endian)][4-byte sequence number (big-endian)][payload bytes]
// This format matches UDP for unified session tracking across transport types.
func EncodeTCP(sessionID uint32, seq uint32, payload []byte) []byte {
	packet := make([]byte, TCPHeaderSize+len(payload))
	binary.BigEndian.PutUint32(packet[:4], sessionID)
	binary.BigEndian.PutUint32(packet[4:8], seq)
	copy(packet[TCPHeaderSize:], payload)
	return packet
}

// DecodeTCP extracts the session ID, sequence number, and payload from a TCP transport packet.
// Returns ErrPacketTooShort if the packet is less than 8 bytes.
func DecodeTCP(packet []byte) (sessionID uint32, seq uint32, payload []byte, err error) {
	if len(packet) < TCPHeaderSize {
		return 0, 0, nil, ErrPacketTooShort
	}
	sessionID = binary.BigEndian.Uint32(packet[:4])
	seq = binary.BigEndian.Uint32(packet[4:8])
	payload = packet[TCPHeaderSize:]
	return sessionID, seq, payload, nil
}

// EncodeUDP creates a packet for UDP transport with session ID, sequence number, and payload.
// Format: [4-byte session_id (big-endian)][4-byte sequence number (big-endian)][payload bytes]
func EncodeUDP(sessionID uint32, seq uint32, payload []byte) []byte {
	packet := make([]byte, UDPHeaderSize+len(payload))
	binary.BigEndian.PutUint32(packet[:4], sessionID)
	binary.BigEndian.PutUint32(packet[4:8], seq)
	copy(packet[UDPHeaderSize:], payload)
	return packet
}

// DecodeUDP extracts the session ID, sequence number, and payload from a UDP transport packet.
// Returns ErrUDPPacketTooShort if the packet is less than 8 bytes.
func DecodeUDP(packet []byte) (sessionID uint32, seq uint32, payload []byte, err error) {
	if len(packet) < UDPHeaderSize {
		return 0, 0, nil, ErrUDPPacketTooShort
	}
	sessionID = binary.BigEndian.Uint32(packet[:4])
	seq = binary.BigEndian.Uint32(packet[4:8])
	payload = packet[UDPHeaderSize:]
	return sessionID, seq, payload, nil
}
