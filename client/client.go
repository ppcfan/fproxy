package client

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"fproxy/config"
	"fproxy/protocol"
)

const maxTCPFrameSize = 65535

// Default response dedup window size
const defaultResponseDedupSize = 1000

var ErrFrameTooLarge = errors.New("frame too large: exceeds 65535 bytes")

// ResponseDedupWindow tracks seen response sequence numbers for deduplication.
type ResponseDedupWindow struct {
	mu      sync.Mutex
	seen    map[uint32]struct{}
	minSeq  uint32
	maxSeq  uint32
	size    int
	started bool
}

// NewResponseDedupWindow creates a new response deduplication window.
func NewResponseDedupWindow(size int) *ResponseDedupWindow {
	return &ResponseDedupWindow{
		seen: make(map[uint32]struct{}),
		size: size,
	}
}

// IsDuplicate returns true if the sequence number has been seen before.
func (d *ResponseDedupWindow) IsDuplicate(seq uint32) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		d.started = true
		d.minSeq = seq
		d.maxSeq = seq
		d.seen[seq] = struct{}{}
		return false
	}

	if _, ok := d.seen[seq]; ok {
		return true
	}

	if seq < d.minSeq {
		return true // Too old, treat as duplicate
	}

	d.seen[seq] = struct{}{}

	if seq > d.maxSeq {
		d.maxSeq = seq
		if len(d.seen) > d.size {
			newMin := d.maxSeq - uint32(d.size) + 1
			for s := range d.seen {
				if s < newMin {
					delete(d.seen, s)
				}
			}
			d.minSeq = newMin
		}
	}

	return false
}

// UDPSession tracks a session for UDP transport.
type UDPSession struct {
	sessionID       uint32
	seq             atomic.Uint32
	lastActivity    time.Time
	responseDedupMu sync.Mutex
	responseDedup   *ResponseDedupWindow
}

// TCPSession tracks a session for TCP transport.
type TCPSession struct {
	conn         net.Conn
	seq          atomic.Uint32
	lastActivity time.Time
	mu           sync.Mutex
	closed       bool
	cancel       context.CancelFunc
}

// Client handles UDP packets from sources and forwards them to servers.
type Client struct {
	cfg        *config.ClientConfig
	logger     *slog.Logger
	listener   *net.UDPConn
	listenAddr string
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc

	// Session ID counter for UDP transport
	sessionIDCounter atomic.Uint32

	// For UDP transport: map sourceAddr -> UDPSession
	udpSessionsMu sync.RWMutex
	udpSessions   map[string]*UDPSession

	// Reverse map for UDP: sessionID -> sourceAddr
	udpSessionAddrsMu sync.RWMutex
	udpSessionAddrs   map[uint32]*net.UDPAddr

	// For TCP transport: map sourceAddr -> TCPSession
	tcpSessionsMu sync.RWMutex
	tcpSessions   map[string]*TCPSession

	// Server endpoints configuration
	serverEndpoints []config.Endpoint

	// UDP senders (one per UDP server endpoint, shared by all sessions)
	udpSendersMu sync.RWMutex
	udpSenders   []*net.UDPConn
}

// New creates a new Client instance.
func New(cfg *config.ClientConfig, logger *slog.Logger) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		cfg:             cfg,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		udpSessions:     make(map[string]*UDPSession),
		udpSessionAddrs: make(map[uint32]*net.UDPAddr),
		tcpSessions:     make(map[string]*TCPSession),
		serverEndpoints: cfg.Servers,
	}
}

// Start begins listening and handling packets.
func (c *Client) Start() error {
	// Create UDP senders for each UDP server endpoint
	for _, ep := range c.cfg.Servers {
		if ep.Protocol == "udp" {
			udpAddr, err := net.ResolveUDPAddr("udp", ep.Address)
			if err != nil {
				c.Stop()
				return err
			}
			conn, err := net.DialUDP("udp", nil, udpAddr)
			if err != nil {
				c.Stop()
				return err
			}
			c.udpSendersMu.Lock()
			c.udpSenders = append(c.udpSenders, conn)
			c.udpSendersMu.Unlock()

			c.logger.Info("connected to UDP server", "addr", ep.Address)

			// Start response receiver for this UDP sender
			c.wg.Add(1)
			go c.handleUDPServerResponse(conn)
		}
	}

	// Start UDP listener for sources
	udpAddr, err := net.ResolveUDPAddr("udp", c.cfg.ListenAddr)
	if err != nil {
		c.Stop()
		return err
	}

	c.listener, err = net.ListenUDP("udp", udpAddr)
	if err != nil {
		c.Stop()
		return err
	}

	c.listenAddr = c.listener.LocalAddr().String()
	c.logger.Info("listening on UDP", "addr", c.listenAddr)

	c.wg.Add(1)
	go c.handleListener()

	// Start session cleanup goroutine
	c.wg.Add(1)
	go c.sessionCleanupLoop()

	return nil
}

func (c *Client) handleListener() {
	defer c.wg.Done()

	buf := make([]byte, 65535)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		n, sourceAddr, err := c.listener.ReadFromUDP(buf)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			c.logger.Error("UDP read error", "error", err)
			continue
		}

		c.handlePacket(sourceAddr, buf[:n])
	}
}

func (c *Client) handlePacket(sourceAddr *net.UDPAddr, payload []byte) {
	sourceKey := sourceAddr.String()

	// Handle UDP transport endpoints
	c.handleUDPTransport(sourceAddr, sourceKey, payload)

	// Handle TCP transport endpoints
	c.handleTCPTransport(sourceAddr, sourceKey, payload)
}

func (c *Client) handleUDPTransport(sourceAddr *net.UDPAddr, sourceKey string, payload []byte) {
	// Get or create session for this source
	session := c.getOrCreateUDPSession(sourceAddr, sourceKey)

	// Get next sequence number for this session
	seq := session.seq.Add(1) - 1

	// Update last activity
	c.udpSessionsMu.Lock()
	session.lastActivity = time.Now()
	c.udpSessionsMu.Unlock()

	// Encode with session ID for UDP transport
	packet := protocol.EncodeUDP(session.sessionID, seq, payload)

	// Send to all UDP server endpoints
	c.udpSendersMu.RLock()
	for i, sender := range c.udpSenders {
		_, err := sender.Write(packet)
		if err != nil {
			c.logger.Error("UDP send error", "endpoint", i, "error", err)
		}
	}
	c.udpSendersMu.RUnlock()

	c.logger.Debug("forwarded packet via UDP", "sessionID", session.sessionID, "seq", seq, "size", len(payload))
}

func (c *Client) getOrCreateUDPSession(sourceAddr *net.UDPAddr, sourceKey string) *UDPSession {
	// Try read lock first
	c.udpSessionsMu.RLock()
	session, exists := c.udpSessions[sourceKey]
	c.udpSessionsMu.RUnlock()

	if exists {
		return session
	}

	// Create new session
	c.udpSessionsMu.Lock()
	defer c.udpSessionsMu.Unlock()

	// Double-check after acquiring write lock
	if session, exists = c.udpSessions[sourceKey]; exists {
		return session
	}

	sessionID := c.sessionIDCounter.Add(1)
	session = &UDPSession{
		sessionID:     sessionID,
		lastActivity:  time.Now(),
		responseDedup: NewResponseDedupWindow(defaultResponseDedupSize),
	}
	c.udpSessions[sourceKey] = session

	// Store reverse mapping
	c.udpSessionAddrsMu.Lock()
	c.udpSessionAddrs[sessionID] = sourceAddr
	c.udpSessionAddrsMu.Unlock()

	c.logger.Info("created UDP session", "sessionID", sessionID, "source", sourceKey)

	return session
}

func (c *Client) handleUDPServerResponse(serverConn *net.UDPConn) {
	defer c.wg.Done()

	buf := make([]byte, 65535)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		// Set read deadline to allow checking context
		serverConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := serverConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if c.ctx.Err() != nil {
				return
			}
			c.logger.Error("UDP server read error", "error", err)
			continue
		}

		// Decode response (includes sessionID and seq for deduplication)
		sessionID, seq, payload, err := protocol.DecodeUDP(buf[:n])
		if err != nil {
			c.logger.Error("UDP response decode error", "error", err)
			continue
		}

		// Lookup source address for this session
		c.udpSessionAddrsMu.RLock()
		sourceAddr := c.udpSessionAddrs[sessionID]
		c.udpSessionAddrsMu.RUnlock()

		if sourceAddr == nil {
			c.logger.Warn("no source address for session", "sessionID", sessionID)
			continue
		}

		// Find session and check for duplicate response
		var session *UDPSession
		c.udpSessionsMu.RLock()
		for _, sess := range c.udpSessions {
			if sess.sessionID == sessionID {
				session = sess
				break
			}
		}
		c.udpSessionsMu.RUnlock()

		if session == nil {
			c.logger.Warn("session not found for response", "sessionID", sessionID)
			continue
		}

		// Check for duplicate response
		if session.responseDedup.IsDuplicate(seq) {
			c.logger.Debug("duplicate UDP response discarded", "sessionID", sessionID, "seq", seq)
			continue
		}

		// Update session activity
		c.udpSessionsMu.Lock()
		session.lastActivity = time.Now()
		c.udpSessionsMu.Unlock()

		// Forward response to source
		_, err = c.listener.WriteToUDP(payload, sourceAddr)
		if err != nil {
			c.logger.Error("failed to forward response to source", "sessionID", sessionID, "error", err)
		} else {
			c.logger.Debug("forwarded response to source", "sessionID", sessionID, "seq", seq, "size", len(payload))
		}
	}
}

func (c *Client) handleTCPTransport(sourceAddr *net.UDPAddr, sourceKey string, payload []byte) {
	// Get the sessionID for this source (shared with UDP transport)
	udpSession := c.getOrCreateUDPSession(sourceAddr, sourceKey)
	sessionID := udpSession.sessionID

	// For each TCP server endpoint, get or create a dedicated TCP session
	for _, ep := range c.serverEndpoints {
		if ep.Protocol != "tcp" {
			continue
		}

		session, created, err := c.getOrCreateTCPSession(sourceAddr, sourceKey, ep.Address)
		if err != nil {
			c.logger.Error("failed to create TCP session", "error", err)
			continue
		}

		if created {
			c.logger.Info("created TCP session", "source", sourceKey, "server", ep.Address)
		}

		// Get next sequence number for this session
		seq := session.seq.Add(1) - 1

		// Update last activity for both sessions
		now := time.Now()
		session.mu.Lock()
		session.lastActivity = now
		session.mu.Unlock()

		// Also update UDP session activity to prevent it from being cleaned up
		c.udpSessionsMu.Lock()
		udpSession.lastActivity = now
		c.udpSessionsMu.Unlock()

		// Encode with TCP format (includes session ID for unified session tracking)
		packet := protocol.EncodeTCP(sessionID, seq, payload)

		// Send with length prefix
		err = c.sendTCPFrame(session, packet)
		if err != nil {
			c.logger.Error("TCP send error", "source", sourceKey, "error", err)
			continue
		}

		c.logger.Debug("forwarded packet via TCP", "sessionID", sessionID, "seq", seq, "size", len(payload))
	}
}

func (c *Client) getOrCreateTCPSession(sourceAddr *net.UDPAddr, sourceKey string, serverAddr string) (*TCPSession, bool, error) {
	// Key includes both source and server address for uniqueness
	sessionKey := sourceKey + "->" + serverAddr

	// Try read lock first
	c.tcpSessionsMu.RLock()
	session, exists := c.tcpSessions[sessionKey]
	c.tcpSessionsMu.RUnlock()

	if exists {
		return session, false, nil
	}

	// Create new session
	c.tcpSessionsMu.Lock()
	defer c.tcpSessionsMu.Unlock()

	// Double-check after acquiring write lock
	if session, exists = c.tcpSessions[sessionKey]; exists {
		return session, false, nil
	}

	// Create new TCP connection
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return nil, false, err
	}

	ctx, cancel := context.WithCancel(c.ctx)
	session = &TCPSession{
		conn:         conn,
		lastActivity: time.Now(),
		cancel:       cancel,
	}
	c.tcpSessions[sessionKey] = session

	// Start response receiver for this TCP connection
	c.wg.Add(1)
	go c.handleTCPServerResponse(ctx, sourceAddr, conn, sessionKey)

	return session, true, nil
}

func (c *Client) sendTCPFrame(session *TCPSession, data []byte) error {
	if len(data) > maxTCPFrameSize {
		return ErrFrameTooLarge
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.closed {
		return io.ErrClosedPipe
	}

	// Create frame with length prefix
	frame := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(data)))
	copy(frame[2:], data)

	// Use loop to handle short writes
	written := 0
	for written < len(frame) {
		n, err := session.conn.Write(frame[written:])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		written += n
	}
	return nil
}

func (c *Client) handleTCPServerResponse(ctx context.Context, sourceAddr *net.UDPAddr, serverConn net.Conn, sessionKey string) {
	defer c.wg.Done()
	defer c.cleanupTCPSession(sessionKey)

	lenBuf := make([]byte, 2)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Set read deadline to allow checking context
		serverConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, err := io.ReadFull(serverConn, lenBuf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil || err == io.EOF {
				return
			}
			c.logger.Error("TCP response read length error", "error", err)
			return
		}

		length := int(binary.BigEndian.Uint16(lenBuf))
		if length == 0 || length > 65535 {
			c.logger.Error("invalid TCP response length", "length", length)
			return
		}

		data := make([]byte, length)
		_, err = io.ReadFull(serverConn, data)
		if err != nil {
			if ctx.Err() == nil {
				c.logger.Error("TCP response read data error", "error", err)
			}
			return
		}

		// Decode response (sessionID identifies the source, seq for deduplication)
		sessionID, seq, payload, err := protocol.DecodeTCP(data)
		if err != nil {
			c.logger.Error("TCP response decode error", "error", err)
			continue
		}

		// Lookup source address by sessionID
		c.udpSessionAddrsMu.RLock()
		resolvedSourceAddr := c.udpSessionAddrs[sessionID]
		c.udpSessionAddrsMu.RUnlock()

		if resolvedSourceAddr == nil {
			c.logger.Warn("no source address for TCP response session", "sessionID", sessionID)
			continue
		}

		// Find session and check for duplicate response (uses shared dedup window)
		var udpSession *UDPSession
		c.udpSessionsMu.RLock()
		for _, sess := range c.udpSessions {
			if sess.sessionID == sessionID {
				udpSession = sess
				break
			}
		}
		c.udpSessionsMu.RUnlock()

		if udpSession == nil {
			c.logger.Warn("session not found for TCP response", "sessionID", sessionID)
			continue
		}

		// Check for duplicate response (shared with UDP path)
		if udpSession.responseDedup.IsDuplicate(seq) {
			c.logger.Debug("duplicate TCP response discarded", "sessionID", sessionID, "seq", seq)
			continue
		}

		// Update session activity for both UDP session (to prevent cleanup) and TCP session
		now := time.Now()

		// Update UDP session activity to prevent it from being cleaned up
		c.udpSessionsMu.Lock()
		udpSession.lastActivity = now
		c.udpSessionsMu.Unlock()

		// Update TCP session activity
		c.tcpSessionsMu.RLock()
		if session, ok := c.tcpSessions[sessionKey]; ok {
			session.mu.Lock()
			session.lastActivity = now
			session.mu.Unlock()
		}
		c.tcpSessionsMu.RUnlock()

		// Forward response to source
		_, err = c.listener.WriteToUDP(payload, resolvedSourceAddr)
		if err != nil {
			c.logger.Error("failed to forward TCP response to source", "sessionID", sessionID, "error", err)
		} else {
			c.logger.Debug("forwarded TCP response to source", "sessionID", sessionID, "seq", seq, "size", len(payload))
		}
	}
}

func (c *Client) cleanupTCPSession(sessionKey string) {
	c.tcpSessionsMu.Lock()
	session, exists := c.tcpSessions[sessionKey]
	if exists {
		delete(c.tcpSessions, sessionKey)
	}
	c.tcpSessionsMu.Unlock()

	if session != nil {
		session.cancel()
		session.conn.Close()
		c.logger.Info("cleaned up TCP session", "session", sessionKey)
	}
}

func (c *Client) sessionCleanupLoop() {
	defer c.wg.Done()

	// Cleanup interval is the smaller of session timeout or 10 seconds
	interval := c.cfg.SessionTimeout
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.cleanupExpiredSessions()
		}
	}
}

func (c *Client) cleanupExpiredSessions() {
	now := time.Now()
	timeout := c.cfg.SessionTimeout

	// Cleanup UDP sessions
	c.udpSessionsMu.Lock()
	for sourceKey, session := range c.udpSessions {
		if now.Sub(session.lastActivity) > timeout {
			c.logger.Info("cleaning up expired UDP session", "sessionID", session.sessionID, "source", sourceKey)
			delete(c.udpSessions, sourceKey)

			// Also remove reverse mapping
			c.udpSessionAddrsMu.Lock()
			delete(c.udpSessionAddrs, session.sessionID)
			c.udpSessionAddrsMu.Unlock()
		}
	}
	c.udpSessionsMu.Unlock()

	// Cleanup TCP sessions
	c.tcpSessionsMu.Lock()
	for sessionKey, session := range c.tcpSessions {
		session.mu.Lock()
		lastActivity := session.lastActivity
		session.mu.Unlock()

		if now.Sub(lastActivity) > timeout {
			c.logger.Info("cleaning up expired TCP session", "session", sessionKey)
			session.cancel()
			session.conn.Close()
			delete(c.tcpSessions, sessionKey)
		}
	}
	c.tcpSessionsMu.Unlock()
}

// Stop gracefully shuts down the client.
func (c *Client) Stop() {
	c.cancel()

	if c.listener != nil {
		c.listener.Close()
	}

	// Close UDP senders
	c.udpSendersMu.Lock()
	for _, sender := range c.udpSenders {
		sender.Close()
	}
	c.udpSendersMu.Unlock()

	// Close TCP sessions
	c.tcpSessionsMu.Lock()
	for _, session := range c.tcpSessions {
		session.cancel()
		session.conn.Close()
	}
	c.tcpSessionsMu.Unlock()

	c.wg.Wait()
	c.logger.Info("client stopped")
}

// ListenAddr returns the actual address the client is listening on.
// Useful when configured with port 0 (dynamic port allocation).
func (c *Client) ListenAddr() string {
	return c.listenAddr
}
