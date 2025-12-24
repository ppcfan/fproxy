package protocol

import (
	"encoding/binary"
	"errors"
)

const HeaderSize = 4

var ErrPacketTooShort = errors.New("packet too short: must be at least 4 bytes")

// Encode creates a packet with the given sequence number and payload.
// Format: [4-byte sequence number (big-endian)][payload bytes]
func Encode(seq uint32, payload []byte) []byte {
	packet := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint32(packet[:HeaderSize], seq)
	copy(packet[HeaderSize:], payload)
	return packet
}

// Decode extracts the sequence number and payload from a packet.
// Returns ErrPacketTooShort if the packet is less than 4 bytes.
func Decode(packet []byte) (seq uint32, payload []byte, err error) {
	if len(packet) < HeaderSize {
		return 0, nil, ErrPacketTooShort
	}
	seq = binary.BigEndian.Uint32(packet[:HeaderSize])
	payload = packet[HeaderSize:]
	return seq, payload, nil
}
