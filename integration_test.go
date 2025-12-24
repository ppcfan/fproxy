package main

import (
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"fproxy/client"
	"fproxy/config"
	"fproxy/server"
)

func TestEndToEndSinglePath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server to receive forwarded packets (dynamic port)
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	var receivedMu sync.Mutex
	receivedPackets := [][]byte{}

	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			receivedMu.Lock()
			receivedPackets = append(receivedPackets, data)
			receivedMu.Unlock()
		}
	}()

	// Start server with dynamic port
	serverCfg := &config.ServerConfig{
		ListenAddrs: []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "udp"}},
		TargetAddr:  targetConn.LocalAddr().String(),
		DedupWindow: 1000,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client with dynamic port, pointing to server's actual address
	clientCfg := &config.ClientConfig{
		ListenAddr: "127.0.0.1:0",
		Servers:    []config.Endpoint{{Address: serverAddr, Protocol: "udp"}},
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Send test packets to client
	sourceConn, err := net.Dial("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	// Send 3 unique packets
	testMessages := []string{"hello", "world", "test123"}
	for _, msg := range testMessages {
		_, err := sourceConn.Write([]byte(msg))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Wait for packets to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify all packets were received at target (set-based comparison for UDP reordering tolerance)
	receivedMu.Lock()
	count := len(receivedPackets)
	receivedSet := make(map[string]bool)
	for _, p := range receivedPackets {
		receivedSet[string(p)] = true
	}
	receivedMu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 packets, got %d", count)
	}

	// Verify all expected messages were received (order-independent)
	for _, want := range testMessages {
		if !receivedSet[want] {
			t.Errorf("missing packet: %q", want)
		}
	}
}

func TestEndToEndMultiPathDeduplication(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server (dynamic port)
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	var receivedMu sync.Mutex
	receivedPackets := [][]byte{}

	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			receivedMu.Lock()
			receivedPackets = append(receivedPackets, data)
			receivedMu.Unlock()
		}
	}()

	// Start server with two UDP listeners (dynamic ports)
	serverCfg := &config.ServerConfig{
		ListenAddrs: []config.Endpoint{
			{Address: "127.0.0.1:0", Protocol: "udp"},
			{Address: "127.0.0.1:0", Protocol: "udp"},
		},
		TargetAddr:  targetConn.LocalAddr().String(),
		DedupWindow: 1000,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddrs := srv.ListenAddrs()
	if len(serverAddrs) != 2 {
		t.Fatalf("expected 2 server addresses, got %d", len(serverAddrs))
	}

	// Start client that sends to both server endpoints (dynamic port)
	clientCfg := &config.ClientConfig{
		ListenAddr: "127.0.0.1:0",
		Servers: []config.Endpoint{
			{Address: serverAddrs[0], Protocol: "udp"},
			{Address: serverAddrs[1], Protocol: "udp"},
		},
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Send test packets to client
	sourceConn, err := net.Dial("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	// Send 5 unique packets with different content
	testMessages := []string{"msg1", "msg2", "msg3", "msg4", "msg5"}
	for _, msg := range testMessages {
		_, err := sourceConn.Write([]byte(msg))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Wait for packets to be processed
	time.Sleep(100 * time.Millisecond)

	// Check deduplication - we should receive exactly 5 packets despite multi-path
	receivedMu.Lock()
	count := len(receivedPackets)
	receivedSet := make(map[string]bool)
	for _, p := range receivedPackets {
		receivedSet[string(p)] = true
	}
	receivedMu.Unlock()

	// Each packet is sent to 2 endpoints, but server should deduplicate
	if count != 5 {
		t.Errorf("expected 5 unique packets after deduplication, got %d", count)
	}

	// Verify all expected messages were received (order-independent)
	for _, want := range testMessages {
		if !receivedSet[want] {
			t.Errorf("missing packet after dedup: %q", want)
		}
	}
}

func TestEndToEndTCPPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server (dynamic port)
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	var receivedMu sync.Mutex
	receivedPackets := [][]byte{}

	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			receivedMu.Lock()
			receivedPackets = append(receivedPackets, data)
			receivedMu.Unlock()
		}
	}()

	// Start server with TCP listener (dynamic port)
	serverCfg := &config.ServerConfig{
		ListenAddrs: []config.Endpoint{
			{Address: "127.0.0.1:0", Protocol: "tcp"},
		},
		TargetAddr:  targetConn.LocalAddr().String(),
		DedupWindow: 1000,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client that sends via TCP (dynamic port)
	clientCfg := &config.ClientConfig{
		ListenAddr: "127.0.0.1:0",
		Servers: []config.Endpoint{
			{Address: serverAddr, Protocol: "tcp"},
		},
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Send test packets to client
	sourceConn, err := net.Dial("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	testMsg := []byte("tcp path test")
	_, err = sourceConn.Write(testMsg)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for packet to be processed
	time.Sleep(100 * time.Millisecond)

	receivedMu.Lock()
	count := len(receivedPackets)
	var firstPacket []byte
	if count > 0 {
		firstPacket = receivedPackets[0]
	}
	receivedMu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 packet via TCP path, got %d", count)
	}

	if count > 0 && string(firstPacket) != string(testMsg) {
		t.Errorf("packet content mismatch: got %q, want %q", firstPacket, testMsg)
	}
}

func TestEndToEndMixedPaths(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server (dynamic port)
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	var receivedMu sync.Mutex
	receivedPackets := [][]byte{}

	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			receivedMu.Lock()
			receivedPackets = append(receivedPackets, data)
			receivedMu.Unlock()
		}
	}()

	// Start server with mixed UDP and TCP listeners (dynamic ports)
	serverCfg := &config.ServerConfig{
		ListenAddrs: []config.Endpoint{
			{Address: "127.0.0.1:0", Protocol: "udp"},
			{Address: "127.0.0.1:0", Protocol: "tcp"},
		},
		TargetAddr:  targetConn.LocalAddr().String(),
		DedupWindow: 1000,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddrs := srv.ListenAddrs()
	if len(serverAddrs) != 2 {
		t.Fatalf("expected 2 server addresses, got %d", len(serverAddrs))
	}

	// Start client that sends to both UDP and TCP endpoints
	clientCfg := &config.ClientConfig{
		ListenAddr: "127.0.0.1:0",
		Servers: []config.Endpoint{
			{Address: serverAddrs[0], Protocol: "udp"},
			{Address: serverAddrs[1], Protocol: "tcp"},
		},
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Send test packets to client
	sourceConn, err := net.Dial("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	// Send packets with unique content
	testMessages := []string{"mixed1", "mixed2", "mixed3"}
	for _, msg := range testMessages {
		_, err := sourceConn.Write([]byte(msg))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Wait for packets to be processed
	time.Sleep(100 * time.Millisecond)

	// With unified session tracking, packets sent via both UDP and TCP paths share
	// the same sessionID and are deduplicated together. So we expect 3 packets total.
	receivedMu.Lock()
	count := len(receivedPackets)
	receivedSet := make(map[string]int)
	for _, p := range receivedPackets {
		receivedSet[string(p)]++
	}
	receivedMu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 packets (deduplicated across UDP+TCP paths), got %d", count)
	}

	// Verify each message was received exactly once (deduplicated across paths)
	for _, want := range testMessages {
		if receivedSet[want] != 1 {
			t.Errorf("expected packet %q to be received 1 time, got %d", want, receivedSet[want])
		}
	}
}

func TestBidirectionalUDPPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back packets with a prefix
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server goroutine
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			// Echo back with "ECHO:" prefix
			response := append([]byte("ECHO:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with UDP listener
	serverCfg := &config.ServerConfig{
		ListenAddrs:    []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "udp"}},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 60 * time.Second,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client with UDP transport
	clientCfg := &config.ClientConfig{
		ListenAddr:     "127.0.0.1:0",
		Servers:        []config.Endpoint{{Address: serverAddr, Protocol: "udp"}},
		SessionTimeout: 60 * time.Second,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Create source UDP connection
	sourceAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sourceConn, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	// Send packet to client
	clientUDPAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}

	testMsg := []byte("hello")
	_, err = sourceConn.WriteToUDP(testMsg, clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for response
	sourceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, _, err := sourceConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected response from target via relay, got error: %v", err)
	}

	expected := "ECHO:hello"
	if string(buf[:n]) != expected {
		t.Errorf("expected response %q, got %q", expected, string(buf[:n]))
	}
}

func TestBidirectionalTCPPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back packets with a prefix
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server goroutine
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			// Echo back with "ECHO:" prefix
			response := append([]byte("ECHO:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with TCP listener
	serverCfg := &config.ServerConfig{
		ListenAddrs:    []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "tcp"}},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 60 * time.Second,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client with TCP transport
	clientCfg := &config.ClientConfig{
		ListenAddr:     "127.0.0.1:0",
		Servers:        []config.Endpoint{{Address: serverAddr, Protocol: "tcp"}},
		SessionTimeout: 60 * time.Second,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Create source UDP connection
	sourceAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sourceConn, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	// Send packet to client
	clientUDPAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}

	testMsg := []byte("hello-tcp")
	_, err = sourceConn.WriteToUDP(testMsg, clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for response
	sourceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, _, err := sourceConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected response from target via TCP relay, got error: %v", err)
	}

	expected := "ECHO:hello-tcp"
	if string(buf[:n]) != expected {
		t.Errorf("expected response %q, got %q", expected, string(buf[:n]))
	}
}

func TestBidirectionalMultiplePackets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back packets
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server goroutine
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := append([]byte("RE:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with UDP listener
	serverCfg := &config.ServerConfig{
		ListenAddrs:    []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "udp"}},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 60 * time.Second,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client
	clientCfg := &config.ClientConfig{
		ListenAddr:     "127.0.0.1:0",
		Servers:        []config.Endpoint{{Address: serverAddr, Protocol: "udp"}},
		SessionTimeout: 60 * time.Second,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()

	// Create source UDP connection
	sourceAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sourceConn, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceConn.Close()

	clientUDPAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Send multiple packets and verify responses
	testMessages := []string{"msg1", "msg2", "msg3"}
	responses := make(map[string]bool)
	responsesMu := sync.Mutex{}

	// Start response receiver
	done := make(chan bool)
	go func() {
		buf := make([]byte, 65535)
		for i := 0; i < len(testMessages); i++ {
			sourceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _, err := sourceConn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			responsesMu.Lock()
			responses[string(buf[:n])] = true
			responsesMu.Unlock()
		}
		done <- true
	}()

	// Send packets
	for _, msg := range testMessages {
		_, err = sourceConn.WriteToUDP([]byte(msg), clientUDPAddr)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond) // Small delay between packets
	}

	// Wait for responses
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for responses")
	}

	// Verify all responses received
	responsesMu.Lock()
	defer responsesMu.Unlock()

	for _, msg := range testMessages {
		expected := "RE:" + msg
		if !responses[expected] {
			t.Errorf("missing response for %q, expected %q", msg, expected)
		}
	}
}

func TestMultipleSourcesSeparateSessions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := append([]byte("R:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server
	serverCfg := &config.ServerConfig{
		ListenAddrs:    []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "udp"}},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 60 * time.Second,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client
	clientCfg := &config.ClientConfig{
		ListenAddr:     "127.0.0.1:0",
		Servers:        []config.Endpoint{{Address: serverAddr, Protocol: "udp"}},
		SessionTimeout: 60 * time.Second,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()
	clientUDPAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Create two separate source connections (simulating two different sources)
	source1Addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	source1, err := net.ListenUDP("udp", source1Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer source1.Close()

	source2Addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	source2, err := net.ListenUDP("udp", source2Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer source2.Close()

	// Send from source1
	_, err = source1.WriteToUDP([]byte("from-source1"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Send from source2
	_, err = source2.WriteToUDP([]byte("from-source2"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Receive responses - each source should get its own response
	buf := make([]byte, 65535)

	source1.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := source1.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("source1 expected response, got error: %v", err)
	}
	if string(buf[:n]) != "R:from-source1" {
		t.Errorf("source1 expected 'R:from-source1', got %q", string(buf[:n]))
	}

	source2.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err = source2.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("source2 expected response, got error: %v", err)
	}
	if string(buf[:n]) != "R:from-source2" {
		t.Errorf("source2 expected 'R:from-source2', got %q", string(buf[:n]))
	}
}

func TestBidirectionalMultiPathSingleClient(t *testing.T) {
	// This test verifies that bidirectional relay works with multi-path:
	// - Single client sends to multiple server ports
	// - Server deduplicates packets
	// - Response goes back through one of the paths
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := append([]byte("ECHO:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with TWO UDP listeners
	serverCfg := &config.ServerConfig{
		ListenAddrs: []config.Endpoint{
			{Address: "127.0.0.1:0", Protocol: "udp"},
			{Address: "127.0.0.1:0", Protocol: "udp"},
		},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 60 * time.Second,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddrs := srv.ListenAddrs()
	if len(serverAddrs) != 2 {
		t.Fatalf("expected 2 server addresses, got %d", len(serverAddrs))
	}

	// Start ONE client that sends to BOTH server UDP ports (multi-path)
	clientCfg := &config.ClientConfig{
		ListenAddr: "127.0.0.1:0",
		Servers: []config.Endpoint{
			{Address: serverAddrs[0], Protocol: "udp"},
			{Address: serverAddrs[1], Protocol: "udp"},
		},
		SessionTimeout: 60 * time.Second,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	// Create source connection
	sourceAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	source, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	clientAddr, _ := net.ResolveUDPAddr("udp", cli.ListenAddr())

	// Send a packet - it will be sent to both server ports
	_, err = source.WriteToUDP([]byte("multi-path-msg"), clientAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Should receive exactly one response (server deduplicates, target echoes once)
	buf := make([]byte, 65535)
	source.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := source.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected response, got error: %v", err)
	}
	if string(buf[:n]) != "ECHO:multi-path-msg" {
		t.Errorf("expected 'ECHO:multi-path-msg', got %q", string(buf[:n]))
	}

	// Verify no additional responses (dedup should prevent duplicates)
	source.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = source.ReadFromUDP(buf)
	if err == nil {
		t.Error("expected timeout (no duplicate response), but got a response")
	}
}

func TestUDPSessionTimeoutCleanup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := append([]byte("R:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with short session timeout (200ms)
	// Cleanup interval will be 200ms (same as timeout, min 100ms)
	serverCfg := &config.ServerConfig{
		ListenAddrs:    []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "udp"}},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 200 * time.Millisecond,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client with short session timeout
	clientCfg := &config.ClientConfig{
		ListenAddr:     "127.0.0.1:0",
		Servers:        []config.Endpoint{{Address: serverAddr, Protocol: "udp"}},
		SessionTimeout: 200 * time.Millisecond,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()
	clientUDPAddr, _ := net.ResolveUDPAddr("udp", clientAddr)

	// Create source connection
	sourceAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	source, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	// Verify no sessions initially
	if srv.SessionCount() != 0 {
		t.Errorf("expected 0 sessions initially, got %d", srv.SessionCount())
	}

	// Send first packet - should create a session
	_, err = source.WriteToUDP([]byte("first"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 65535)
	source.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := source.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected first response, got error: %v", err)
	}
	if string(buf[:n]) != "R:first" {
		t.Errorf("expected 'R:first', got %q", string(buf[:n]))
	}

	// Verify session was created
	if srv.SessionCount() != 1 {
		t.Errorf("expected 1 session after first packet, got %d", srv.SessionCount())
	}

	// Wait for session timeout + cleanup interval (~400-500ms should be enough)
	time.Sleep(500 * time.Millisecond)

	// Verify session was cleaned up
	if srv.SessionCount() != 0 {
		t.Errorf("expected 0 sessions after timeout, got %d", srv.SessionCount())
	}

	// Send second packet - should work with new session
	_, err = source.WriteToUDP([]byte("second"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	source.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err = source.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected second response after session recreated, got error: %v", err)
	}
	if string(buf[:n]) != "R:second" {
		t.Errorf("expected 'R:second', got %q", string(buf[:n]))
	}

	// Verify new session was created
	if srv.SessionCount() != 1 {
		t.Errorf("expected 1 session after second packet, got %d", srv.SessionCount())
	}
}

// TestMultiPathResponseRedundancy verifies that responses are sent via all paths
// but the client deduplicates and only forwards one copy to the source.
func TestMultiPathResponseRedundancy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server that sends 3 responses for each request
	// (simulates multiple responses that should be deduplicated)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := append([]byte("MULTI:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with BOTH UDP and TCP listeners
	serverCfg := &config.ServerConfig{
		ListenAddrs: []config.Endpoint{
			{Address: "127.0.0.1:0", Protocol: "udp"},
			{Address: "127.0.0.1:0", Protocol: "udp"},
			{Address: "127.0.0.1:0", Protocol: "tcp"},
		},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 60 * time.Second,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddrs := srv.ListenAddrs()

	// Start client connecting to all server endpoints
	clientCfg := &config.ClientConfig{
		ListenAddr: "127.0.0.1:0",
		Servers: []config.Endpoint{
			{Address: serverAddrs[0], Protocol: "udp"},
			{Address: serverAddrs[1], Protocol: "udp"},
			{Address: serverAddrs[2], Protocol: "tcp"},
		},
		SessionTimeout: 60 * time.Second,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()
	clientUDPAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Create source connection
	sourceAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	source, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	// Send request
	_, err = source.WriteToUDP([]byte("test-multi-path"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	// Wait a bit for response to arrive via all paths
	time.Sleep(200 * time.Millisecond)

	// Count how many responses we receive (should be exactly 1 due to deduplication)
	source.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 65535)
	responseCount := 0

	for {
		n, _, err := source.ReadFromUDP(buf)
		if err != nil {
			break // Timeout or error, stop counting
		}
		expectedResponse := "MULTI:test-multi-path"
		if string(buf[:n]) != expectedResponse {
			t.Errorf("unexpected response: got %q, want %q", string(buf[:n]), expectedResponse)
		}
		responseCount++
	}

	// Client should deduplicate responses - source receives exactly 1
	if responseCount != 1 {
		t.Errorf("expected exactly 1 response (after client deduplication), got %d", responseCount)
	}
}

func TestTCPSessionTimeoutCleanup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start target UDP server that echoes back
	targetAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetConn, err := net.ListenUDP("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()

	// Echo server
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := targetConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := append([]byte("R:"), buf[:n]...)
			targetConn.WriteToUDP(response, addr)
		}
	}()

	// Start server with TCP listener and short session timeout (200ms)
	serverCfg := &config.ServerConfig{
		ListenAddrs:    []config.Endpoint{{Address: "127.0.0.1:0", Protocol: "tcp"}},
		TargetAddr:     targetConn.LocalAddr().String(),
		DedupWindow:    1000,
		SessionTimeout: 200 * time.Millisecond,
	}
	srv := server.New(serverCfg, logger)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	serverAddr := srv.ListenAddrs()[0]

	// Start client with TCP transport and short session timeout
	clientCfg := &config.ClientConfig{
		ListenAddr:     "127.0.0.1:0",
		Servers:        []config.Endpoint{{Address: serverAddr, Protocol: "tcp"}},
		SessionTimeout: 200 * time.Millisecond,
	}
	cli := client.New(clientCfg, logger)
	if err := cli.Start(); err != nil {
		t.Fatal(err)
	}
	defer cli.Stop()

	clientAddr := cli.ListenAddr()
	clientUDPAddr, _ := net.ResolveUDPAddr("udp", clientAddr)

	// Create source connection
	sourceAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	source, err := net.ListenUDP("udp", sourceAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	// Verify no sessions initially
	if srv.SessionCount() != 0 {
		t.Errorf("expected 0 sessions initially, got %d", srv.SessionCount())
	}

	// Send first packet - should create a session on server
	_, err = source.WriteToUDP([]byte("tcp-first"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 65535)
	source.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := source.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected first response, got error: %v", err)
	}
	if string(buf[:n]) != "R:tcp-first" {
		t.Errorf("expected 'R:tcp-first', got %q", string(buf[:n]))
	}

	// Verify session was created on server
	if srv.SessionCount() != 1 {
		t.Errorf("expected 1 session after first packet, got %d", srv.SessionCount())
	}

	// Wait for session timeout + cleanup interval
	time.Sleep(500 * time.Millisecond)

	// Verify session was cleaned up
	if srv.SessionCount() != 0 {
		t.Errorf("expected 0 sessions after timeout, got %d", srv.SessionCount())
	}

	// Send second packet - should work with new session
	_, err = source.WriteToUDP([]byte("tcp-second"), clientUDPAddr)
	if err != nil {
		t.Fatal(err)
	}

	source.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err = source.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected second response after session recreated, got error: %v", err)
	}
	if string(buf[:n]) != "R:tcp-second" {
		t.Errorf("expected 'R:tcp-second', got %q", string(buf[:n]))
	}
}
