package server

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"fproxy/config"
	"fproxy/protocol"
)

// DedupWindow tracks seen sequence numbers for deduplication.
type DedupWindow struct {
	mu      sync.Mutex
	seen    map[uint32]struct{}
	minSeq  uint32
	maxSeq  uint32
	size    int
	started bool
}

// NewDedupWindow creates a new deduplication window with the given size.
func NewDedupWindow(size int) *DedupWindow {
	return &DedupWindow{
		seen: make(map[uint32]struct{}),
		size: size,
	}
}

// IsDuplicate returns true if the sequence number has been seen before.
// It also updates the window state.
func (d *DedupWindow) IsDuplicate(seq uint32) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		d.started = true
		d.minSeq = seq
		d.maxSeq = seq
		d.seen[seq] = struct{}{}
		return false
	}

	// Check if already seen
	if _, ok := d.seen[seq]; ok {
		return true
	}

	// Check if sequence is too old (before window)
	if seq < d.minSeq {
		// Packet is before the window, treat as duplicate
		return true
	}

	// Mark as seen
	d.seen[seq] = struct{}{}

	// Slide window if needed
	if seq > d.maxSeq {
		d.maxSeq = seq
		// Clean up old entries if window is too large
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

// TargetSession represents a session with a dedicated target connection.
type TargetSession struct {
	targetConn   *net.UDPConn
	dedupWindow  *DedupWindow
	responseSeq  atomic.Uint32 // sequence number for responses
	lastActivity time.Time
	cancel       context.CancelFunc
}

// ClientPath represents a client communication path (UDP or TCP).
type ClientPath struct {
	pathType string        // "udp" or "tcp"
	udpAddr  *net.UDPAddr  // For UDP paths: client address
	udpConn  *net.UDPConn  // For UDP paths: listener
	tcpConn  net.Conn      // For TCP paths: connection
}

// Server handles incoming UDP/TCP packets and forwards them to the target.
type Server struct {
	cfg         *config.ServerConfig
	logger      *slog.Logger
	listeners   []io.Closer
	listenAddrs []string
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc

	// Unified sessions by sessionID (shared by UDP and TCP)
	sessionsMu sync.RWMutex
	sessions   map[uint32]*TargetSession

	// Client paths per sessionID (all active UDP/TCP paths for responses)
	clientPathsMu sync.RWMutex
	clientPaths   map[uint32][]*ClientPath

	// Track which sessionID each TCP connection belongs to (for cleanup)
	tcpConnToSessionMu sync.RWMutex
	tcpConnToSession   map[net.Conn]uint32
}

// New creates a new Server instance.
func New(cfg *config.ServerConfig, logger *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:              cfg,
		logger:           logger,
		ctx:              ctx,
		cancel:           cancel,
		sessions:         make(map[uint32]*TargetSession),
		clientPaths:      make(map[uint32][]*ClientPath),
		tcpConnToSession: make(map[net.Conn]uint32),
	}
}

// Start begins listening on all configured endpoints.
func (s *Server) Start() error {
	// Start listeners for each endpoint
	for _, ep := range s.cfg.ListenAddrs {
		switch ep.Protocol {
		case "udp":
			if err := s.startUDPListener(ep.Address); err != nil {
				s.Stop()
				return err
			}
		case "tcp":
			if err := s.startTCPListener(ep.Address); err != nil {
				s.Stop()
				return err
			}
		}
	}

	// Start session cleanup goroutine
	s.wg.Add(1)
	go s.sessionCleanupLoop()

	return nil
}

func (s *Server) startUDPListener(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}

	actualAddr := conn.LocalAddr().String()
	s.listeners = append(s.listeners, conn)
	s.listenAddrs = append(s.listenAddrs, actualAddr)

	s.logger.Info("listening on UDP", "addr", actualAddr)

	s.wg.Add(1)
	go s.handleUDPListener(conn)

	return nil
}

func (s *Server) handleUDPListener(conn *net.UDPConn) {
	defer s.wg.Done()

	buf := make([]byte, 65535)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.logger.Error("UDP read error", "error", err)
			continue
		}

		s.handleUDPPacket(conn, clientAddr, buf[:n])
	}
}

func (s *Server) handleUDPPacket(listener *net.UDPConn, clientAddr *net.UDPAddr, data []byte) {
	sessionID, seq, payload, err := protocol.DecodeUDP(data)
	if err != nil {
		s.logger.Error("UDP packet decode error", "error", err)
		return
	}

	// Get or create target session for this sessionID (shared by UDP and TCP)
	session, created := s.getOrCreateSession(sessionID)
	if session == nil {
		s.logger.Error("failed to create session", "sessionID", sessionID)
		return
	}

	if created {
		s.logger.Debug("created new session", "sessionID", sessionID)
	}

	// Update last activity
	s.sessionsMu.Lock()
	session.lastActivity = time.Now()
	s.sessionsMu.Unlock()

	// Check for duplicate within this session
	if session.dedupWindow.IsDuplicate(seq) {
		s.logger.Debug("duplicate packet discarded", "sessionID", sessionID, "seq", seq)
		return
	}

	// Add/update UDP path for this session (for multi-path responses)
	s.addUDPPath(sessionID, clientAddr, listener)

	// Forward payload to target
	_, err = session.targetConn.Write(payload)
	if err != nil {
		s.logger.Error("failed to forward to target", "sessionID", sessionID, "error", err)
		return
	}

	s.logger.Debug("forwarded UDP packet", "sessionID", sessionID, "seq", seq, "size", len(payload))
}

func (s *Server) getOrCreateSession(sessionID uint32) (*TargetSession, bool) {
	// Try read lock first for existing session
	s.sessionsMu.RLock()
	session, exists := s.sessions[sessionID]
	s.sessionsMu.RUnlock()

	if exists {
		return session, false
	}

	// Need to create new session
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	// Double-check after acquiring write lock
	if session, exists = s.sessions[sessionID]; exists {
		return session, false
	}

	// Create new target connection
	targetAddr, err := net.ResolveUDPAddr("udp", s.cfg.TargetAddr)
	if err != nil {
		s.logger.Error("failed to resolve target address", "error", err)
		return nil, false
	}

	targetConn, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		s.logger.Error("failed to connect to target", "error", err)
		return nil, false
	}

	ctx, cancel := context.WithCancel(s.ctx)
	session = &TargetSession{
		targetConn:   targetConn,
		dedupWindow:  NewDedupWindow(s.cfg.DedupWindow),
		lastActivity: time.Now(),
		cancel:       cancel,
	}

	s.sessions[sessionID] = session

	// Start response handler for this session
	s.wg.Add(1)
	go s.handleTargetResponse(ctx, sessionID, targetConn)

	s.logger.Info("created target session", "sessionID", sessionID, "target", s.cfg.TargetAddr)

	return session, true
}

// addUDPPath adds or updates a UDP path for the given session.
func (s *Server) addUDPPath(sessionID uint32, clientAddr *net.UDPAddr, listener *net.UDPConn) {
	s.clientPathsMu.Lock()
	defer s.clientPathsMu.Unlock()

	paths := s.clientPaths[sessionID]

	// Check if this UDP path already exists (same listener)
	for _, p := range paths {
		if p.pathType == "udp" && p.udpConn == listener {
			// Update client address (might have changed)
			p.udpAddr = clientAddr
			return
		}
	}

	// Add new UDP path
	s.clientPaths[sessionID] = append(paths, &ClientPath{
		pathType: "udp",
		udpAddr:  clientAddr,
		udpConn:  listener,
	})
}

// handleTargetResponse reads responses from target and sends to ALL client paths.
func (s *Server) handleTargetResponse(ctx context.Context, sessionID uint32, targetConn *net.UDPConn) {
	defer s.wg.Done()

	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Set read deadline to allow checking context
		targetConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := targetConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			s.logger.Error("target read error", "sessionID", sessionID, "error", err)
			return
		}

		// Update last activity
		s.sessionsMu.Lock()
		if session, ok := s.sessions[sessionID]; ok {
			session.lastActivity = time.Now()
		}
		s.sessionsMu.Unlock()

		// Get response sequence number
		s.sessionsMu.RLock()
		session := s.sessions[sessionID]
		s.sessionsMu.RUnlock()
		if session == nil {
			continue
		}
		seq := session.responseSeq.Add(1) - 1

		// Get all client paths for this session
		s.clientPathsMu.RLock()
		paths := s.clientPaths[sessionID]
		// Make a copy to avoid holding lock during I/O
		pathsCopy := make([]*ClientPath, len(paths))
		copy(pathsCopy, paths)
		s.clientPathsMu.RUnlock()

		if len(pathsCopy) == 0 {
			s.logger.Warn("no client paths for session", "sessionID", sessionID)
			continue
		}

		// Send response to ALL paths (multi-path redundancy)
		for _, path := range pathsCopy {
			if path.pathType == "udp" {
				// Encode response with sessionID and seq for UDP
				response := protocol.EncodeUDP(sessionID, seq, buf[:n])
				_, err = path.udpConn.WriteToUDP(response, path.udpAddr)
				if err != nil {
					s.logger.Error("failed to send UDP response", "sessionID", sessionID, "error", err)
				}
			} else if path.pathType == "tcp" {
				// Encode response with sessionID and seq for TCP
				response := protocol.EncodeTCP(sessionID, seq, buf[:n])
				// Send with length prefix
				frame := make([]byte, 2+len(response))
				binary.BigEndian.PutUint16(frame[:2], uint16(len(response)))
				copy(frame[2:], response)
				// Use loop to handle short writes (like client sendTCPFrame)
				written := 0
				for written < len(frame) {
					wn, werr := path.tcpConn.Write(frame[written:])
					if werr != nil {
						s.logger.Error("failed to send TCP response", "sessionID", sessionID, "error", werr)
						break
					}
					if wn == 0 {
						s.logger.Error("TCP short write", "sessionID", sessionID)
						break
					}
					written += wn
				}
			}
		}

		s.logger.Debug("sent response to all paths", "sessionID", sessionID, "seq", seq, "paths", len(pathsCopy), "size", n)
	}
}

func (s *Server) startTCPListener(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	actualAddr := ln.Addr().String()
	s.listeners = append(s.listeners, ln)
	s.listenAddrs = append(s.listenAddrs, actualAddr)
	s.logger.Info("listening on TCP", "addr", actualAddr)

	s.wg.Add(1)
	go s.handleTCPListener(ln)

	return nil
}

func (s *Server) handleTCPListener(ln net.Listener) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.logger.Error("TCP accept error", "error", err)
			continue
		}

		s.wg.Add(1)
		go s.handleTCPConn(conn)
	}
}

func (s *Server) handleTCPConn(conn net.Conn) {
	defer s.wg.Done()
	defer s.cleanupTCPConn(conn)

	s.logger.Info("TCP connection established", "client", conn.RemoteAddr())

	// For TCP, we need length-prefixed messages
	// Format: [2-byte length (big-endian)][packet data]
	lenBuf := make([]byte, 2)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		_, err := io.ReadFull(conn, lenBuf)
		if err != nil {
			if err != io.EOF && s.ctx.Err() == nil {
				s.logger.Error("TCP read length error", "error", err)
			}
			return
		}

		length := int(lenBuf[0])<<8 | int(lenBuf[1])
		if length == 0 || length > 65535 {
			s.logger.Error("invalid TCP packet length", "length", length)
			return
		}

		data := make([]byte, length)
		_, err = io.ReadFull(conn, data)
		if err != nil {
			if s.ctx.Err() == nil {
				s.logger.Error("TCP read data error", "error", err)
			}
			return
		}

		s.handleTCPPacket(conn, data)
	}
}

func (s *Server) cleanupTCPConn(conn net.Conn) {
	// Get sessionID for this connection
	s.tcpConnToSessionMu.RLock()
	sessionID, exists := s.tcpConnToSession[conn]
	s.tcpConnToSessionMu.RUnlock()

	if exists {
		// Remove this TCP path from clientPaths
		s.clientPathsMu.Lock()
		paths := s.clientPaths[sessionID]
		for i, p := range paths {
			if p.pathType == "tcp" && p.tcpConn == conn {
				// Remove this path
				s.clientPaths[sessionID] = append(paths[:i], paths[i+1:]...)
				break
			}
		}
		s.clientPathsMu.Unlock()

		// Remove from tcpConnToSession
		s.tcpConnToSessionMu.Lock()
		delete(s.tcpConnToSession, conn)
		s.tcpConnToSessionMu.Unlock()
	}

	conn.Close()
	s.logger.Info("TCP connection closed", "client", conn.RemoteAddr())
}

func (s *Server) handleTCPPacket(conn net.Conn, data []byte) {
	// Decode packet with sessionID (unified format)
	sessionID, seq, payload, err := protocol.DecodeTCP(data)
	if err != nil {
		s.logger.Error("TCP packet decode error", "error", err)
		return
	}

	// Get or create session (shared with UDP)
	session, created := s.getOrCreateSession(sessionID)
	if session == nil {
		s.logger.Error("failed to create session", "sessionID", sessionID)
		return
	}

	if created {
		s.logger.Debug("created new session via TCP", "sessionID", sessionID)
	}

	// Update last activity
	s.sessionsMu.Lock()
	session.lastActivity = time.Now()
	s.sessionsMu.Unlock()

	// Check for duplicate within this session
	if session.dedupWindow.IsDuplicate(seq) {
		s.logger.Debug("duplicate TCP packet discarded", "sessionID", sessionID, "seq", seq)
		return
	}

	// Add TCP path for this session (for multi-path responses)
	s.addTCPPath(sessionID, conn)

	// Forward payload to target
	_, err = session.targetConn.Write(payload)
	if err != nil {
		s.logger.Error("failed to forward TCP packet to target", "sessionID", sessionID, "error", err)
		return
	}

	s.logger.Debug("forwarded TCP packet", "sessionID", sessionID, "seq", seq, "size", len(payload))
}

// addTCPPath adds a TCP path for the given session.
func (s *Server) addTCPPath(sessionID uint32, conn net.Conn) {
	// First, track the sessionID for this connection
	s.tcpConnToSessionMu.Lock()
	s.tcpConnToSession[conn] = sessionID
	s.tcpConnToSessionMu.Unlock()

	s.clientPathsMu.Lock()
	defer s.clientPathsMu.Unlock()

	paths := s.clientPaths[sessionID]

	// Check if this TCP path already exists
	for _, p := range paths {
		if p.pathType == "tcp" && p.tcpConn == conn {
			return // Already tracked
		}
	}

	// Add new TCP path
	s.clientPaths[sessionID] = append(paths, &ClientPath{
		pathType: "tcp",
		tcpConn:  conn,
	})
}

func (s *Server) sessionCleanupLoop() {
	defer s.wg.Done()

	// Cleanup interval is the smaller of session timeout or 10 seconds
	// This ensures timely cleanup for short timeouts while not being too aggressive for long ones
	interval := s.cfg.SessionTimeout
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond // Minimum interval to avoid busy loop
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpiredSessions()
		}
	}
}

func (s *Server) cleanupExpiredSessions() {
	now := time.Now()
	timeout := s.cfg.SessionTimeout

	// Cleanup unified sessions
	s.sessionsMu.Lock()
	for sessionID, session := range s.sessions {
		if now.Sub(session.lastActivity) > timeout {
			s.logger.Info("cleaning up expired session", "sessionID", sessionID)
			session.cancel()
			session.targetConn.Close()
			delete(s.sessions, sessionID)

			// Also remove client paths and TCP connection mappings
			s.clientPathsMu.Lock()
			paths := s.clientPaths[sessionID]
			for _, p := range paths {
				if p.pathType == "tcp" && p.tcpConn != nil {
					s.tcpConnToSessionMu.Lock()
					delete(s.tcpConnToSession, p.tcpConn)
					s.tcpConnToSessionMu.Unlock()
					// Note: TCP connections are not closed here; they'll be closed
					// when the read loop fails or client disconnects
				}
			}
			delete(s.clientPaths, sessionID)
			s.clientPathsMu.Unlock()
		}
	}
	s.sessionsMu.Unlock()
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	s.cancel()

	// Close all listeners
	for _, ln := range s.listeners {
		ln.Close()
	}

	// Close all unified sessions
	s.sessionsMu.Lock()
	for sessionID, session := range s.sessions {
		session.cancel()
		session.targetConn.Close()
		delete(s.sessions, sessionID)
	}
	s.sessionsMu.Unlock()

	// Clear client paths
	s.clientPathsMu.Lock()
	for sessionID := range s.clientPaths {
		delete(s.clientPaths, sessionID)
	}
	s.clientPathsMu.Unlock()

	// Clear TCP connection mappings
	s.tcpConnToSessionMu.Lock()
	for conn := range s.tcpConnToSession {
		delete(s.tcpConnToSession, conn)
	}
	s.tcpConnToSessionMu.Unlock()

	s.wg.Wait()
	s.logger.Info("server stopped")
}

// ListenAddrs returns the actual addresses the server is listening on.
// Useful when configured with port 0 (dynamic port allocation).
func (s *Server) ListenAddrs() []string {
	return s.listenAddrs
}

// SessionCount returns the number of active sessions.
func (s *Server) SessionCount() int {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return len(s.sessions)
}
