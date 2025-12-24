package client

import (
	"io"
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"fproxy/config"
)

func TestSequenceNumberAssignment(t *testing.T) {
	t.Run("sequence starts at zero", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		cfg := &config.ClientConfig{
			ListenAddr: ":0",
			Servers:    []config.Endpoint{{Address: "localhost:9999", Protocol: "udp"}},
		}
		c := New(cfg, logger)

		if c.NextSeq() != 0 {
			t.Errorf("initial sequence should be 0, got %d", c.NextSeq())
		}
	})

	t.Run("sequence increments atomically", func(t *testing.T) {
		var seq atomic.Uint32

		// Simulate concurrent sequence assignment
		done := make(chan bool)
		count := 1000

		for i := 0; i < 10; i++ {
			go func() {
				for j := 0; j < count; j++ {
					seq.Add(1)
				}
				done <- true
			}()
		}

		for i := 0; i < 10; i++ {
			<-done
		}

		if seq.Load() != uint32(10*count) {
			t.Errorf("expected %d, got %d", 10*count, seq.Load())
		}
	})
}

type mockSender struct {
	data   [][]byte
	closed bool
}

func (m *mockSender) Send(data []byte) error {
	cpy := make([]byte, len(data))
	copy(cpy, data)
	m.data = append(m.data, cpy)
	return nil
}

func (m *mockSender) Close() error {
	m.closed = true
	return nil
}

func TestClientSendsToAllEndpoints(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr: ":0",
		Servers:    []config.Endpoint{},
	}

	c := New(cfg, logger)

	// Replace senders with mocks
	mock1 := &mockSender{}
	mock2 := &mockSender{}
	c.senders = []Sender{mock1, mock2}

	// Handle a test packet
	c.handlePacket([]byte("hello"))

	// Both senders should have received the packet
	if len(mock1.data) != 1 {
		t.Errorf("mock1 expected 1 packet, got %d", len(mock1.data))
	}
	if len(mock2.data) != 1 {
		t.Errorf("mock2 expected 1 packet, got %d", len(mock2.data))
	}

	// Packets should be identical (same sequence number)
	if len(mock1.data) > 0 && len(mock2.data) > 0 {
		if string(mock1.data[0]) != string(mock2.data[0]) {
			t.Error("packets sent to different endpoints should be identical")
		}
	}
}

func TestClientStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr: ":0",
		Servers:    []config.Endpoint{},
	}

	c := New(cfg, logger)

	mock := &mockSender{}
	c.senders = []Sender{mock}

	c.Stop()

	if !mock.closed {
		t.Error("sender should be closed after Stop()")
	}
}

func TestTCPSenderOversizedFrame(t *testing.T) {
	// Create a TCP sender with a mock connection
	sender := &TCPSender{
		addr:   "test:1234",
		conn:   nil, // won't be used due to early return
		closed: false,
	}

	// Create oversized data (> 65535 bytes)
	oversizedData := make([]byte, 65536)

	err := sender.Send(oversizedData)
	if err != ErrFrameTooLarge {
		t.Errorf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestTCPSenderMaxSizeFrame(t *testing.T) {
	mock := &mockConn{}
	sender := &TCPSender{
		addr:   "test:1234",
		conn:   mock,
		closed: false,
	}

	// Exactly at limit should work
	maxData := make([]byte, 65535)
	err := sender.Send(maxData)
	if err != nil {
		t.Errorf("max size frame should succeed, got %v", err)
	}
}

type mockConn struct {
	written []byte
}

func (m *mockConn) Write(b []byte) (int, error) {
	m.written = append(m.written, b...)
	return len(b), nil
}

func (m *mockConn) Read(b []byte) (int, error)         { return 0, nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestTCPSenderFrameFormat(t *testing.T) {
	mock := &mockConn{}
	sender := &TCPSender{
		addr:   "test:1234",
		conn:   mock,
		closed: false,
	}

	testData := []byte("hello")
	err := sender.Send(testData)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Verify frame format: [2-byte length][data]
	if len(mock.written) != 2+len(testData) {
		t.Errorf("frame length = %d, want %d", len(mock.written), 2+len(testData))
	}

	// Verify length prefix (big-endian)
	length := int(mock.written[0])<<8 | int(mock.written[1])
	if length != len(testData) {
		t.Errorf("length prefix = %d, want %d", length, len(testData))
	}

	// Verify payload
	if string(mock.written[2:]) != string(testData) {
		t.Errorf("payload = %q, want %q", mock.written[2:], testData)
	}
}

func TestTCPSenderSingleWrite(t *testing.T) {
	// Track number of Write calls
	writeCount := 0
	countingConn := &countingMockConn{writeCount: &writeCount, bytesPerWrite: -1}

	sender := &TCPSender{
		addr:   "test:1234",
		conn:   countingConn,
		closed: false,
	}

	testData := []byte("test")
	sender.Send(testData)

	// Should be exactly 1 write call when full write succeeds
	if writeCount != 1 {
		t.Errorf("expected 1 Write call, got %d", writeCount)
	}
}

func TestTCPSenderShortWrite(t *testing.T) {
	// Simulate short writes (1 byte at a time)
	writeCount := 0
	written := []byte{}
	shortWriteConn := &countingMockConn{
		writeCount:    &writeCount,
		bytesPerWrite: 1,
		written:       &written,
	}

	sender := &TCPSender{
		addr:   "test:1234",
		conn:   shortWriteConn,
		closed: false,
	}

	testData := []byte("hello")
	err := sender.Send(testData)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Should have made multiple write calls
	expectedWrites := 2 + len(testData) // 2 byte length + payload
	if writeCount != expectedWrites {
		t.Errorf("expected %d Write calls for short writes, got %d", expectedWrites, writeCount)
	}

	// Verify all data was written correctly
	if len(written) != 2+len(testData) {
		t.Errorf("total written = %d, want %d", len(written), 2+len(testData))
	}
}

func TestTCPSenderZeroWrite(t *testing.T) {
	// Simulate zero-length write (stuck connection)
	zeroWriteConn := &countingMockConn{
		writeCount:    new(int),
		bytesPerWrite: 0,
	}

	sender := &TCPSender{
		addr:   "test:1234",
		conn:   zeroWriteConn,
		closed: false,
	}

	err := sender.Send([]byte("test"))
	if err != io.ErrShortWrite {
		t.Errorf("expected io.ErrShortWrite, got %v", err)
	}
}

type countingMockConn struct {
	writeCount    *int
	bytesPerWrite int // -1 means write all, 0 means write nothing, >0 means write that many bytes
	written       *[]byte
}

func (m *countingMockConn) Write(b []byte) (int, error) {
	*m.writeCount++
	n := len(b)
	if m.bytesPerWrite >= 0 && m.bytesPerWrite < n {
		n = m.bytesPerWrite
	}
	if m.written != nil {
		*m.written = append(*m.written, b[:n]...)
	}
	return n, nil
}

func (m *countingMockConn) Read(b []byte) (int, error)         { return 0, nil }
func (m *countingMockConn) Close() error                       { return nil }
func (m *countingMockConn) LocalAddr() net.Addr                { return nil }
func (m *countingMockConn) RemoteAddr() net.Addr               { return nil }
func (m *countingMockConn) SetDeadline(t time.Time) error      { return nil }
func (m *countingMockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *countingMockConn) SetWriteDeadline(t time.Time) error { return nil }
