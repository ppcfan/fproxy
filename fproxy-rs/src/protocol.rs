//! Packet encoding and decoding for UDP/TCP transport.
//!
//! Both UDP and TCP use the same packet format:
//! `[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]`

use thiserror::Error;

/// Protocol header size for both TCP and UDP transport: session_id (4) + seq (4).
pub const HEADER_SIZE: usize = 8;

/// Errors that can occur during packet decoding.
#[derive(Error, Debug, PartialEq)]
pub enum ProtocolError {
    #[error("packet too short: must be at least {HEADER_SIZE} bytes")]
    PacketTooShort,
}

/// Encodes a TCP transport packet with session ID, sequence number, and payload.
///
/// Format: `[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]`
///
/// Note: TCP framing (2-byte length prefix) is handled by the sender/receiver, not here.
#[inline]
pub fn encode_tcp(session_id: u32, seq: u32, payload: &[u8]) -> Vec<u8> {
    encode_packet(session_id, seq, payload)
}

/// Decodes a TCP transport packet, extracting session ID, sequence number, and payload.
///
/// Returns `ProtocolError::PacketTooShort` if the packet is less than 8 bytes.
#[inline]
pub fn decode_tcp(packet: &[u8]) -> Result<(u32, u32, &[u8]), ProtocolError> {
    decode_packet(packet)
}

/// Encodes a UDP transport packet with session ID, sequence number, and payload.
///
/// Format: `[4-byte session_id (big-endian)][4-byte seq (big-endian)][payload]`
#[inline]
pub fn encode_udp(session_id: u32, seq: u32, payload: &[u8]) -> Vec<u8> {
    encode_packet(session_id, seq, payload)
}

/// Decodes a UDP transport packet, extracting session ID, sequence number, and payload.
///
/// Returns `ProtocolError::PacketTooShort` if the packet is less than 8 bytes.
#[inline]
pub fn decode_udp(packet: &[u8]) -> Result<(u32, u32, &[u8]), ProtocolError> {
    decode_packet(packet)
}

/// Internal function to encode a packet (shared by TCP and UDP).
fn encode_packet(session_id: u32, seq: u32, payload: &[u8]) -> Vec<u8> {
    let mut packet = Vec::with_capacity(HEADER_SIZE + payload.len());
    packet.extend_from_slice(&session_id.to_be_bytes());
    packet.extend_from_slice(&seq.to_be_bytes());
    packet.extend_from_slice(payload);
    packet
}

/// Internal function to decode a packet (shared by TCP and UDP).
fn decode_packet(packet: &[u8]) -> Result<(u32, u32, &[u8]), ProtocolError> {
    if packet.len() < HEADER_SIZE {
        return Err(ProtocolError::PacketTooShort);
    }

    let session_id = u32::from_be_bytes([packet[0], packet[1], packet[2], packet[3]]);
    let seq = u32::from_be_bytes([packet[4], packet[5], packet[6], packet[7]]);
    let payload = &packet[HEADER_SIZE..];

    Ok((session_id, seq, payload))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_encode_decode_tcp() {
        let session_id = 0x12345678;
        let seq = 0xABCDEF01;
        let payload = b"hello world";

        let encoded = encode_tcp(session_id, seq, payload);

        assert_eq!(encoded.len(), HEADER_SIZE + payload.len());
        assert_eq!(&encoded[0..4], &[0x12, 0x34, 0x56, 0x78]);
        assert_eq!(&encoded[4..8], &[0xAB, 0xCD, 0xEF, 0x01]);
        assert_eq!(&encoded[8..], payload.as_slice());

        let (decoded_session_id, decoded_seq, decoded_payload) = decode_tcp(&encoded).unwrap();
        assert_eq!(decoded_session_id, session_id);
        assert_eq!(decoded_seq, seq);
        assert_eq!(decoded_payload, payload);
    }

    #[test]
    fn test_encode_decode_udp() {
        let session_id = 1;
        let seq = 42;
        let payload = b"test payload";

        let encoded = encode_udp(session_id, seq, payload);
        let (decoded_session_id, decoded_seq, decoded_payload) = decode_udp(&encoded).unwrap();

        assert_eq!(decoded_session_id, session_id);
        assert_eq!(decoded_seq, seq);
        assert_eq!(decoded_payload, payload);
    }

    #[test]
    fn test_decode_empty_payload() {
        let session_id = 100;
        let seq = 200;
        let payload: &[u8] = b"";

        let encoded = encode_tcp(session_id, seq, payload);
        assert_eq!(encoded.len(), HEADER_SIZE);

        let (decoded_session_id, decoded_seq, decoded_payload) = decode_tcp(&encoded).unwrap();
        assert_eq!(decoded_session_id, session_id);
        assert_eq!(decoded_seq, seq);
        assert!(decoded_payload.is_empty());
    }

    #[test]
    fn test_decode_too_short() {
        let short_packet = [0u8; 7];
        assert_eq!(
            decode_tcp(&short_packet),
            Err(ProtocolError::PacketTooShort)
        );
        assert_eq!(
            decode_udp(&short_packet),
            Err(ProtocolError::PacketTooShort)
        );

        let empty_packet: &[u8] = &[];
        assert_eq!(decode_tcp(empty_packet), Err(ProtocolError::PacketTooShort));
    }

    #[test]
    fn test_decode_exactly_header_size() {
        let packet = [0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02];
        let (session_id, seq, payload) = decode_tcp(&packet).unwrap();
        assert_eq!(session_id, 1);
        assert_eq!(seq, 2);
        assert!(payload.is_empty());
    }

    #[test]
    fn test_big_endian_encoding() {
        let session_id = 0x01020304u32;
        let seq = 0x05060708u32;
        let payload = b"";

        let encoded = encode_tcp(session_id, seq, payload);

        assert_eq!(encoded[0], 0x01);
        assert_eq!(encoded[1], 0x02);
        assert_eq!(encoded[2], 0x03);
        assert_eq!(encoded[3], 0x04);
        assert_eq!(encoded[4], 0x05);
        assert_eq!(encoded[5], 0x06);
        assert_eq!(encoded[6], 0x07);
        assert_eq!(encoded[7], 0x08);
    }

    #[test]
    fn test_large_payload() {
        let session_id = 1;
        let seq = 1;
        let payload = vec![0xFFu8; 65535];

        let encoded = encode_udp(session_id, seq, &payload);
        assert_eq!(encoded.len(), HEADER_SIZE + 65535);

        let (_, _, decoded_payload) = decode_udp(&encoded).unwrap();
        assert_eq!(decoded_payload.len(), 65535);
        assert!(decoded_payload.iter().all(|&b| b == 0xFF));
    }
}
