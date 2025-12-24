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

	// Should receive exactly 3 unique packets despite being sent over both UDP and TCP
	receivedMu.Lock()
	count := len(receivedPackets)
	receivedSet := make(map[string]bool)
	for _, p := range receivedPackets {
		receivedSet[string(p)] = true
	}
	receivedMu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 unique packets with mixed paths, got %d", count)
	}

	// Verify all expected messages were received
	for _, want := range testMessages {
		if !receivedSet[want] {
			t.Errorf("missing packet: %q", want)
		}
	}
}
