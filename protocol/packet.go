package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	// HeaderSize is the protocol header size: [8-byte session_id][4-byte seq]
	// Used for both TCP and UDP transport.
	HeaderSize = 12
)

var (
	ErrPacketTooShort = errors.New("packet too short: must be at least 12 bytes")
)

// EncodeTCP creates a packet for TCP transport with session ID, sequence number, and payload.
// Format: [8-byte session_id (big-endian)][4-byte sequence number (big-endian)][payload bytes]
// This format matches UDP for unified session tracking across transport types.
func EncodeTCP(sessionID uint64, seq uint32, payload []byte) []byte {
	packet := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint64(packet[:8], sessionID)
	binary.BigEndian.PutUint32(packet[8:12], seq)
	copy(packet[HeaderSize:], payload)
	return packet
}

// DecodeTCP extracts the session ID, sequence number, and payload from a TCP transport packet.
// Returns ErrPacketTooShort if the packet is less than 12 bytes.
func DecodeTCP(packet []byte) (sessionID uint64, seq uint32, payload []byte, err error) {
	if len(packet) < HeaderSize {
		return 0, 0, nil, ErrPacketTooShort
	}
	sessionID = binary.BigEndian.Uint64(packet[:8])
	seq = binary.BigEndian.Uint32(packet[8:12])
	payload = packet[HeaderSize:]
	return sessionID, seq, payload, nil
}

// EncodeUDP creates a packet for UDP transport with session ID, sequence number, and payload.
// Format: [8-byte session_id (big-endian)][4-byte sequence number (big-endian)][payload bytes]
func EncodeUDP(sessionID uint64, seq uint32, payload []byte) []byte {
	packet := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint64(packet[:8], sessionID)
	binary.BigEndian.PutUint32(packet[8:12], seq)
	copy(packet[HeaderSize:], payload)
	return packet
}

// DecodeUDP extracts the session ID, sequence number, and payload from a UDP transport packet.
// Returns ErrPacketTooShort if the packet is less than 12 bytes.
func DecodeUDP(packet []byte) (sessionID uint64, seq uint32, payload []byte, err error) {
	if len(packet) < HeaderSize {
		return 0, 0, nil, ErrPacketTooShort
	}
	sessionID = binary.BigEndian.Uint64(packet[:8])
	seq = binary.BigEndian.Uint32(packet[8:12])
	payload = packet[HeaderSize:]
	return sessionID, seq, payload, nil
}
