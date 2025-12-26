package protocol

import (
	"bytes"
	"testing"
)

func TestEncodeTCP(t *testing.T) {
	tests := []struct {
		name      string
		sessionID uint64
		seq       uint32
		payload   []byte
		want      []byte
	}{
		{
			name:      "zero session and seq, empty payload",
			sessionID: 0,
			seq:       0,
			payload:   []byte{},
			want:      []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:      "session 1, seq 0 with payload",
			sessionID: 1,
			seq:       0,
			payload:   []byte("hello"),
			want:      []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 'h', 'e', 'l', 'l', 'o'},
		},
		{
			name:      "max session and seq",
			sessionID: 0xFFFFFFFFFFFFFFFF,
			seq:       0xFFFFFFFF,
			payload:   []byte{0xAB},
			want:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xAB},
		},
		{
			name:      "session 256, seq 1",
			sessionID: 256,
			seq:       1,
			payload:   []byte("test"),
			want:      []byte{0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 't', 'e', 's', 't'},
		},
		{
			name:      "verify big-endian encoding",
			sessionID: 0x0102030405060708,
			seq:       0x090A0B0C,
			payload:   []byte{},
			want:      []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeTCP(tt.sessionID, tt.seq, tt.payload)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("EncodeTCP(%d, %d, %v) = %v, want %v", tt.sessionID, tt.seq, tt.payload, got, tt.want)
			}
		})
	}
}

func TestDecodeTCP(t *testing.T) {
	tests := []struct {
		name          string
		packet        []byte
		wantSessionID uint64
		wantSeq       uint32
		wantPayload   []byte
		wantErr       error
	}{
		{
			name:          "minimum valid packet",
			packet:        []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			wantSessionID: 0,
			wantSeq:       0,
			wantPayload:   []byte{},
			wantErr:       nil,
		},
		{
			name:          "packet with payload",
			packet:        []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 2, 'h', 'e', 'l', 'l', 'o'},
			wantSessionID: 1,
			wantSeq:       2,
			wantPayload:   []byte("hello"),
			wantErr:       nil,
		},
		{
			name:          "max session and seq",
			packet:        []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xAB},
			wantSessionID: 0xFFFFFFFFFFFFFFFF,
			wantSeq:       0xFFFFFFFF,
			wantPayload:   []byte{0xAB},
			wantErr:       nil,
		},
		{
			name:          "verify big-endian decoding",
			packet:        []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C},
			wantSessionID: 0x0102030405060708,
			wantSeq:       0x090A0B0C,
			wantPayload:   []byte{},
			wantErr:       nil,
		},
		{
			name:    "too short - empty",
			packet:  []byte{},
			wantErr: ErrPacketTooShort,
		},
		{
			name:    "too short - 11 bytes",
			packet:  []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			wantErr: ErrPacketTooShort,
		},
		{
			name:    "too short - 8 bytes (old header size)",
			packet:  []byte{0, 0, 0, 0, 0, 0, 0, 0},
			wantErr: ErrPacketTooShort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID, seq, payload, err := DecodeTCP(tt.packet)
			if err != tt.wantErr {
				t.Errorf("DecodeTCP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr != nil {
				return
			}
			if sessionID != tt.wantSessionID {
				t.Errorf("DecodeTCP() sessionID = %d, want %d", sessionID, tt.wantSessionID)
			}
			if seq != tt.wantSeq {
				t.Errorf("DecodeTCP() seq = %d, want %d", seq, tt.wantSeq)
			}
			if !bytes.Equal(payload, tt.wantPayload) {
				t.Errorf("DecodeTCP() payload = %v, want %v", payload, tt.wantPayload)
			}
		})
	}
}

func TestEncodeUDP(t *testing.T) {
	tests := []struct {
		name      string
		sessionID uint64
		seq       uint32
		payload   []byte
		want      []byte
	}{
		{
			name:      "zero session and seq, empty payload",
			sessionID: 0,
			seq:       0,
			payload:   []byte{},
			want:      []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:      "session 1, seq 0 with payload",
			sessionID: 1,
			seq:       0,
			payload:   []byte("hello"),
			want:      []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 'h', 'e', 'l', 'l', 'o'},
		},
		{
			name:      "session 256, seq 1",
			sessionID: 256,
			seq:       1,
			payload:   []byte("test"),
			want:      []byte{0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 't', 'e', 's', 't'},
		},
		{
			name:      "max session and seq",
			sessionID: 0xFFFFFFFFFFFFFFFF,
			seq:       0xFFFFFFFF,
			payload:   []byte{0xAB},
			want:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xAB},
		},
		{
			name:      "verify big-endian encoding",
			sessionID: 0x0102030405060708,
			seq:       0x090A0B0C,
			payload:   []byte{},
			want:      []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeUDP(tt.sessionID, tt.seq, tt.payload)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("EncodeUDP(%d, %d, %v) = %v, want %v", tt.sessionID, tt.seq, tt.payload, got, tt.want)
			}
		})
	}
}

func TestDecodeUDP(t *testing.T) {
	tests := []struct {
		name          string
		packet        []byte
		wantSessionID uint64
		wantSeq       uint32
		wantPayload   []byte
		wantErr       error
	}{
		{
			name:          "minimum valid packet",
			packet:        []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			wantSessionID: 0,
			wantSeq:       0,
			wantPayload:   []byte{},
			wantErr:       nil,
		},
		{
			name:          "packet with payload",
			packet:        []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 2, 'h', 'e', 'l', 'l', 'o'},
			wantSessionID: 1,
			wantSeq:       2,
			wantPayload:   []byte("hello"),
			wantErr:       nil,
		},
		{
			name:          "max session and seq",
			packet:        []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xAB},
			wantSessionID: 0xFFFFFFFFFFFFFFFF,
			wantSeq:       0xFFFFFFFF,
			wantPayload:   []byte{0xAB},
			wantErr:       nil,
		},
		{
			name:          "verify big-endian decoding",
			packet:        []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C},
			wantSessionID: 0x0102030405060708,
			wantSeq:       0x090A0B0C,
			wantPayload:   []byte{},
			wantErr:       nil,
		},
		{
			name:    "too short - empty",
			packet:  []byte{},
			wantErr: ErrPacketTooShort,
		},
		{
			name:    "too short - 11 bytes",
			packet:  []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			wantErr: ErrPacketTooShort,
		},
		{
			name:    "too short - 8 bytes (old header size)",
			packet:  []byte{0, 0, 0, 0, 0, 0, 0, 0},
			wantErr: ErrPacketTooShort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID, seq, payload, err := DecodeUDP(tt.packet)
			if err != tt.wantErr {
				t.Errorf("DecodeUDP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr != nil {
				return
			}
			if sessionID != tt.wantSessionID {
				t.Errorf("DecodeUDP() sessionID = %d, want %d", sessionID, tt.wantSessionID)
			}
			if seq != tt.wantSeq {
				t.Errorf("DecodeUDP() seq = %d, want %d", seq, tt.wantSeq)
			}
			if !bytes.Equal(payload, tt.wantPayload) {
				t.Errorf("DecodeUDP() payload = %v, want %v", payload, tt.wantPayload)
			}
		})
	}
}

func TestUDPEncodeDecodeRoundTrip(t *testing.T) {
	testCases := []struct {
		sessionID uint64
		seq       uint32
		payload   []byte
	}{
		{0, 0, []byte{}},
		{1, 0, []byte("test")},
		{100, 1000, []byte("longer payload with more data")},
		{0xFFFFFFFFFFFFFFFF, 0xFFFFFFFF, []byte{0, 1, 2, 3, 4, 5}},
		{0x123456789ABCDEF0, 0x9ABCDEF0, []byte("session test")},
	}

	for _, tc := range testCases {
		encoded := EncodeUDP(tc.sessionID, tc.seq, tc.payload)
		sessionID, seq, payload, err := DecodeUDP(encoded)
		if err != nil {
			t.Errorf("UDP round trip failed for sessionID=%d, seq=%d: %v", tc.sessionID, tc.seq, err)
			continue
		}
		if sessionID != tc.sessionID {
			t.Errorf("UDP round trip sessionID mismatch: got %d, want %d", sessionID, tc.sessionID)
		}
		if seq != tc.seq {
			t.Errorf("UDP round trip seq mismatch: got %d, want %d", seq, tc.seq)
		}
		if !bytes.Equal(payload, tc.payload) {
			t.Errorf("UDP round trip payload mismatch: got %v, want %v", payload, tc.payload)
		}
	}
}

func TestTCPEncodeDecodeRoundTrip(t *testing.T) {
	testCases := []struct {
		sessionID uint64
		seq       uint32
		payload   []byte
	}{
		{0, 0, []byte{}},
		{1, 0, []byte("test")},
		{100, 1000, []byte("longer payload with more data")},
		{0xFFFFFFFFFFFFFFFF, 0xFFFFFFFF, []byte{0, 1, 2, 3, 4, 5}},
		{0x123456789ABCDEF0, 0x9ABCDEF0, []byte("session test")},
	}

	for _, tc := range testCases {
		encoded := EncodeTCP(tc.sessionID, tc.seq, tc.payload)
		sessionID, seq, payload, err := DecodeTCP(encoded)
		if err != nil {
			t.Errorf("TCP round trip failed for sessionID=%d, seq=%d: %v", tc.sessionID, tc.seq, err)
			continue
		}
		if sessionID != tc.sessionID {
			t.Errorf("TCP round trip sessionID mismatch: got %d, want %d", sessionID, tc.sessionID)
		}
		if seq != tc.seq {
			t.Errorf("TCP round trip seq mismatch: got %d, want %d", seq, tc.seq)
		}
		if !bytes.Equal(payload, tc.payload) {
			t.Errorf("TCP round trip payload mismatch: got %v, want %v", payload, tc.payload)
		}
	}
}
