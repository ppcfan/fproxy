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
		// Test that atomic sequence counter starts at zero
		var seq atomic.Uint32
		if seq.Load() != 0 {
			t.Errorf("initial sequence should be 0, got %d", seq.Load())
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

func TestClientNew(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{{Address: "localhost:9999", Protocol: "udp"}},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	if c.cfg != cfg {
		t.Error("config not set correctly")
	}
	if c.logger != logger {
		t.Error("logger not set correctly")
	}
	if c.udpSessions == nil {
		t.Error("udpSessions map should be initialized")
	}
	if c.tcpSessions == nil {
		t.Error("tcpSessions map should be initialized")
	}
}

func TestClientStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}

	c := New(cfg, logger)

	// Start should succeed even with no servers
	err := c.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Stop should complete without error
	c.Stop()
}

func TestSendTCPFrameOversizedFrame(t *testing.T) {
	session := &TCPSession{
		conn:   nil, // won't be used due to early return
		closed: false,
	}

	// Create oversized data (> 65535 bytes)
	oversizedData := make([]byte, 65536)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	err := c.sendTCPFrame(session, oversizedData)
	if err != ErrFrameTooLarge {
		t.Errorf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestSendTCPFrameMaxSizeFrame(t *testing.T) {
	mock := &mockConn{}
	session := &TCPSession{
		conn:   mock,
		closed: false,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	// Exactly at limit should work
	maxData := make([]byte, 65535)
	err := c.sendTCPFrame(session, maxData)
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

func TestSendTCPFrameFormat(t *testing.T) {
	mock := &mockConn{}
	session := &TCPSession{
		conn:   mock,
		closed: false,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	testData := []byte("hello")
	err := c.sendTCPFrame(session, testData)
	if err != nil {
		t.Fatalf("sendTCPFrame() error = %v", err)
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

func TestSendTCPFrameSingleWrite(t *testing.T) {
	// Track number of Write calls
	writeCount := 0
	countingConn := &countingMockConn{writeCount: &writeCount, bytesPerWrite: -1}

	session := &TCPSession{
		conn:   countingConn,
		closed: false,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	testData := []byte("test")
	c.sendTCPFrame(session, testData)

	// Should be exactly 1 write call when full write succeeds
	if writeCount != 1 {
		t.Errorf("expected 1 Write call, got %d", writeCount)
	}
}

func TestSendTCPFrameShortWrite(t *testing.T) {
	// Simulate short writes (1 byte at a time)
	writeCount := 0
	written := []byte{}
	shortWriteConn := &countingMockConn{
		writeCount:    &writeCount,
		bytesPerWrite: 1,
		written:       &written,
	}

	session := &TCPSession{
		conn:   shortWriteConn,
		closed: false,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	testData := []byte("hello")
	err := c.sendTCPFrame(session, testData)
	if err != nil {
		t.Fatalf("sendTCPFrame() error = %v", err)
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

func TestSendTCPFrameZeroWrite(t *testing.T) {
	// Simulate zero-length write (stuck connection)
	zeroWriteConn := &countingMockConn{
		writeCount:    new(int),
		bytesPerWrite: 0,
	}

	session := &TCPSession{
		conn:   zeroWriteConn,
		closed: false,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	err := c.sendTCPFrame(session, []byte("test"))
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

func TestUDPSessionCreation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ClientConfig{
		ListenAddr:     ":0",
		Servers:        []config.Endpoint{},
		SessionTimeout: 60 * time.Second,
	}
	c := New(cfg, logger)

	sourceAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000}
	sourceKey := sourceAddr.String()

	// First call should create a new session
	session1 := c.getOrCreateUDPSession(sourceAddr, sourceKey)
	if session1 == nil {
		t.Fatal("session should be created")
	}
	if session1.sessionID == 0 {
		t.Error("session ID should be non-zero")
	}

	// Second call should return the same session
	session2 := c.getOrCreateUDPSession(sourceAddr, sourceKey)
	if session2 != session1 {
		t.Error("should return same session for same source")
	}
	if session2.sessionID != session1.sessionID {
		t.Error("session ID should be the same")
	}

	// Different source should get a different session
	sourceAddr2 := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5001}
	sourceKey2 := sourceAddr2.String()
	session3 := c.getOrCreateUDPSession(sourceAddr2, sourceKey2)
	if session3 == session1 {
		t.Error("different source should get different session")
	}
	if session3.sessionID == session1.sessionID {
		t.Error("different session should have different ID")
	}
}
