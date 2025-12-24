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

	"fproxy/config"
	"fproxy/protocol"
)

const maxTCPFrameSize = 65535

var ErrFrameTooLarge = errors.New("frame too large: exceeds 65535 bytes")

type Client struct {
	cfg        *config.ClientConfig
	logger     *slog.Logger
	seq        atomic.Uint32
	listener   *net.UDPConn
	listenAddr string
	senders    []Sender
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
}

type Sender interface {
	Send(data []byte) error
	Close() error
}

type UDPSender struct {
	conn *net.UDPConn
}

func NewUDPSender(addr string) (*UDPSender, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}
	return &UDPSender{conn: conn}, nil
}

func (s *UDPSender) Send(data []byte) error {
	_, err := s.conn.Write(data)
	return err
}

func (s *UDPSender) Close() error {
	return s.conn.Close()
}

type TCPSender struct {
	addr   string
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

func NewTCPSender(addr string) (*TCPSender, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TCPSender{addr: addr, conn: conn}, nil
}

func (s *TCPSender) Send(data []byte) error {
	if len(data) > maxTCPFrameSize {
		return ErrFrameTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return io.ErrClosedPipe
	}

	// TCP needs length-prefixed messages
	// Format: [2-byte length (big-endian)][packet data]
	// Combine into single buffer to avoid partial frame on write error
	frame := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(data)))
	copy(frame[2:], data)

	// Use loop to handle short writes
	written := 0
	for written < len(frame) {
		n, err := s.conn.Write(frame[written:])
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

func (s *TCPSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.conn.Close()
}

func New(cfg *config.ClientConfig, logger *slog.Logger) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		cfg:    cfg,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (c *Client) Start() error {
	// Create senders for each server endpoint
	for _, ep := range c.cfg.Servers {
		var sender Sender
		var err error

		switch ep.Protocol {
		case "udp":
			sender, err = NewUDPSender(ep.Address)
		case "tcp":
			sender, err = NewTCPSender(ep.Address)
		}

		if err != nil {
			c.Stop()
			return err
		}

		c.senders = append(c.senders, sender)
		c.logger.Info("connected to server", "addr", ep.Address, "protocol", ep.Protocol)
	}

	// Start UDP listener
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

		n, _, err := c.listener.ReadFromUDP(buf)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			c.logger.Error("UDP read error", "error", err)
			continue
		}

		c.handlePacket(buf[:n])
	}
}

func (c *Client) handlePacket(payload []byte) {
	// Assign sequence number
	seq := c.seq.Add(1) - 1

	// Encode packet with sequence number
	packet := protocol.Encode(seq, payload)

	// Send to all server endpoints
	for i, sender := range c.senders {
		if err := sender.Send(packet); err != nil {
			c.logger.Error("send error", "endpoint", i, "error", err)
		}
	}

	c.logger.Debug("forwarded packet", "seq", seq, "size", len(payload))
}

func (c *Client) NextSeq() uint32 {
	return c.seq.Load()
}

func (c *Client) Stop() {
	c.cancel()

	if c.listener != nil {
		c.listener.Close()
	}

	for _, sender := range c.senders {
		sender.Close()
	}

	c.wg.Wait()
	c.logger.Info("client stopped")
}

// ListenAddr returns the actual address the client is listening on.
// Useful when configured with port 0 (dynamic port allocation).
func (c *Client) ListenAddr() string {
	return c.listenAddr
}
