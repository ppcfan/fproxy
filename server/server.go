package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"

	"fproxy/config"
	"fproxy/protocol"
)

type Server struct {
	cfg         *config.ServerConfig
	logger      *slog.Logger
	targetConn  *net.UDPConn
	dedup       *DedupWindow
	listeners   []io.Closer
	listenAddrs []string
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

type DedupWindow struct {
	mu       sync.Mutex
	seen     map[uint32]struct{}
	minSeq   uint32
	maxSeq   uint32
	size     int
	started  bool
}

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

func New(cfg *config.ServerConfig, logger *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:    cfg,
		logger: logger,
		dedup:  NewDedupWindow(cfg.DedupWindow),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) Start() error {
	// Connect to target UDP server
	targetAddr, err := net.ResolveUDPAddr("udp", s.cfg.TargetAddr)
	if err != nil {
		return err
	}
	s.targetConn, err = net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		return err
	}

	s.logger.Info("connected to target", "addr", s.cfg.TargetAddr)

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

		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.logger.Error("UDP read error", "error", err)
			continue
		}

		s.handlePacket(buf[:n])
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
	defer conn.Close()

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

		s.handlePacket(data)
	}
}

func (s *Server) handlePacket(data []byte) {
	seq, payload, err := protocol.Decode(data)
	if err != nil {
		s.logger.Error("packet decode error", "error", err)
		return
	}

	if s.dedup.IsDuplicate(seq) {
		s.logger.Debug("duplicate packet discarded", "seq", seq)
		return
	}

	_, err = s.targetConn.Write(payload)
	if err != nil {
		s.logger.Error("failed to forward to target", "error", err)
		return
	}

	s.logger.Debug("forwarded packet", "seq", seq, "size", len(payload))
}

func (s *Server) Stop() {
	s.cancel()

	for _, ln := range s.listeners {
		ln.Close()
	}

	if s.targetConn != nil {
		s.targetConn.Close()
	}

	s.wg.Wait()
	s.logger.Info("server stopped")
}

// ListenAddrs returns the actual addresses the server is listening on.
// Useful when configured with port 0 (dynamic port allocation).
func (s *Server) ListenAddrs() []string {
	return s.listenAddrs
}
