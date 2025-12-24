package protocol

import (
	"bytes"
	"testing"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name    string
		seq     uint32
		payload []byte
		want    []byte
	}{
		{
			name:    "zero sequence empty payload",
			seq:     0,
			payload: []byte{},
			want:    []byte{0, 0, 0, 0},
		},
		{
			name:    "sequence 1 with payload",
			seq:     1,
			payload: []byte("hello"),
			want:    []byte{0, 0, 0, 1, 'h', 'e', 'l', 'l', 'o'},
		},
		{
			name:    "max sequence",
			seq:     0xFFFFFFFF,
			payload: []byte{0xAB},
			want:    []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xAB},
		},
		{
			name:    "sequence 256",
			seq:     256,
			payload: []byte("test"),
			want:    []byte{0, 0, 1, 0, 't', 'e', 's', 't'},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Encode(tt.seq, tt.payload)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Encode(%d, %v) = %v, want %v", tt.seq, tt.payload, got, tt.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name        string
		packet      []byte
		wantSeq     uint32
		wantPayload []byte
		wantErr     error
	}{
		{
			name:        "minimum valid packet",
			packet:      []byte{0, 0, 0, 0},
			wantSeq:     0,
			wantPayload: []byte{},
			wantErr:     nil,
		},
		{
			name:        "packet with payload",
			packet:      []byte{0, 0, 0, 1, 'h', 'e', 'l', 'l', 'o'},
			wantSeq:     1,
			wantPayload: []byte("hello"),
			wantErr:     nil,
		},
		{
			name:        "max sequence",
			packet:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xAB},
			wantSeq:     0xFFFFFFFF,
			wantPayload: []byte{0xAB},
			wantErr:     nil,
		},
		{
			name:    "too short - empty",
			packet:  []byte{},
			wantErr: ErrPacketTooShort,
		},
		{
			name:    "too short - 3 bytes",
			packet:  []byte{0, 0, 0},
			wantErr: ErrPacketTooShort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seq, payload, err := Decode(tt.packet)
			if err != tt.wantErr {
				t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr != nil {
				return
			}
			if seq != tt.wantSeq {
				t.Errorf("Decode() seq = %d, want %d", seq, tt.wantSeq)
			}
			if !bytes.Equal(payload, tt.wantPayload) {
				t.Errorf("Decode() payload = %v, want %v", payload, tt.wantPayload)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	testCases := []struct {
		seq     uint32
		payload []byte
	}{
		{0, []byte{}},
		{1, []byte("test")},
		{1000, []byte("longer payload with more data")},
		{0xFFFFFFFF, []byte{0, 1, 2, 3, 4, 5}},
	}

	for _, tc := range testCases {
		encoded := Encode(tc.seq, tc.payload)
		seq, payload, err := Decode(encoded)
		if err != nil {
			t.Errorf("Round trip failed for seq=%d: %v", tc.seq, err)
			continue
		}
		if seq != tc.seq {
			t.Errorf("Round trip seq mismatch: got %d, want %d", seq, tc.seq)
		}
		if !bytes.Equal(payload, tc.payload) {
			t.Errorf("Round trip payload mismatch: got %v, want %v", payload, tc.payload)
		}
	}
}
