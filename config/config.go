package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Mode string

const (
	ModeClient Mode = "client"
	ModeServer Mode = "server"
)

type Endpoint struct {
	Address  string
	Protocol string // "udp" or "tcp"
}

// isValidIP checks if a string is a valid IP address (IPv4 or IPv6).
func isValidIP(host string) bool {
	return net.ParseIP(host) != nil
}

// extractHost extracts the host part from an address in "host:port" or ":port" format.
func extractHost(address string) (string, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid address format %q: %w", address, err)
	}
	return host, nil
}

// parseHostIP extracts and parses the host IP from an address.
// Returns (nil, nil) for empty host (e.g., ":8001").
// Returns (nil, error) if the address format is invalid or host is not a valid IP.
func parseHostIP(address string) (net.IP, error) {
	host, err := extractHost(address)
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address %q", host)
	}
	return ip, nil
}

func ParseEndpoint(s string) (Endpoint, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return Endpoint{}, fmt.Errorf("invalid endpoint format %q: expected address/protocol", s)
	}
	proto := strings.ToLower(parts[1])
	if proto != "udp" && proto != "tcp" {
		return Endpoint{}, fmt.Errorf("invalid protocol %q: must be 'udp' or 'tcp'", proto)
	}

	// Validate that the host is an IP address, not a domain name
	host, err := extractHost(parts[0])
	if err != nil {
		return Endpoint{}, err
	}
	// Allow empty host (e.g., ":8001" for listen addresses)
	if host != "" && !isValidIP(host) {
		return Endpoint{}, fmt.Errorf("invalid address %q: %w", parts[0], ErrInvalidIPAddress)
	}

	return Endpoint{Address: parts[0], Protocol: proto}, nil
}

func ParseEndpoints(s string) ([]Endpoint, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	endpoints := make([]Endpoint, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ep, err := ParseEndpoint(p)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, nil
}

type ClientConfig struct {
	ListenAddr     string        `yaml:"listen_addr"`
	Servers        []Endpoint    `yaml:"-"`
	ServersRaw     string        `yaml:"servers"`
	SessionTimeout time.Duration `yaml:"session_timeout"`
}

type ServerConfig struct {
	ListenAddrs    []Endpoint    `yaml:"-"`
	ListenAddrsRaw string        `yaml:"listen_addrs"`
	TargetAddr     string        `yaml:"target_addr"`
	DedupWindow    int           `yaml:"dedup_window"`
	SessionTimeout time.Duration `yaml:"session_timeout"`
}

type Config struct {
	Mode    Mode          `yaml:"mode"`
	Verbose bool          `yaml:"verbose"`
	Client  *ClientConfig `yaml:"client,omitempty"`
	Server  *ServerConfig `yaml:"server,omitempty"`
}

const (
	DefaultDedupWindow    = 10000
	DefaultSessionTimeout = 60 * time.Second
)

var (
	ErrModeRequired               = errors.New("mode is required: must be 'client' or 'server'")
	ErrInvalidMode                = errors.New("invalid mode: must be 'client' or 'server'")
	ErrClientListenRequired       = errors.New("client listen address is required")
	ErrClientServersRequired      = errors.New("client server endpoints are required")
	ErrServerListenRequired       = errors.New("server listen addresses are required")
	ErrServerTargetRequired       = errors.New("server target address is required")
	ErrInvalidIPAddress           = errors.New("server address must be an IP address, not a domain name")
	ErrClientServersDifferentIPs  = errors.New("all server endpoints must have the same IP address: deduplication cannot work across separate servers")
)

func (c *Config) Validate() error {
	if c.Mode == "" {
		return ErrModeRequired
	}
	if c.Mode != ModeClient && c.Mode != ModeServer {
		return ErrInvalidMode
	}

	if c.Mode == ModeClient {
		if c.Client == nil {
			c.Client = &ClientConfig{}
		}
		if c.Client.ListenAddr == "" {
			return ErrClientListenRequired
		}
		if len(c.Client.Servers) == 0 {
			return ErrClientServersRequired
		}
		// Validate all server endpoints have the same IP address
		if len(c.Client.Servers) > 1 {
			firstIP, err := parseHostIP(c.Client.Servers[0].Address)
			if err != nil {
				return fmt.Errorf("invalid server address %q: %w", c.Client.Servers[0].Address, err)
			}
			for i := 1; i < len(c.Client.Servers); i++ {
				ip, err := parseHostIP(c.Client.Servers[i].Address)
				if err != nil {
					return fmt.Errorf("invalid server address %q: %w", c.Client.Servers[i].Address, err)
				}
				if !firstIP.Equal(ip) {
					return ErrClientServersDifferentIPs
				}
			}
		}
		if c.Client.SessionTimeout <= 0 {
			c.Client.SessionTimeout = DefaultSessionTimeout
		}
	}

	if c.Mode == ModeServer {
		if c.Server == nil {
			c.Server = &ServerConfig{}
		}
		if len(c.Server.ListenAddrs) == 0 {
			return ErrServerListenRequired
		}
		if c.Server.TargetAddr == "" {
			return ErrServerTargetRequired
		}
		if c.Server.DedupWindow <= 0 {
			c.Server.DedupWindow = DefaultDedupWindow
		}
		if c.Server.SessionTimeout <= 0 {
			c.Server.SessionTimeout = DefaultSessionTimeout
		}
	}

	return nil
}

func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Parse endpoint strings
	if cfg.Client != nil && cfg.Client.ServersRaw != "" {
		endpoints, err := ParseEndpoints(cfg.Client.ServersRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid client servers: %w", err)
		}
		cfg.Client.Servers = endpoints
	}
	if cfg.Server != nil && cfg.Server.ListenAddrsRaw != "" {
		endpoints, err := ParseEndpoints(cfg.Server.ListenAddrsRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid server listen addresses: %w", err)
		}
		cfg.Server.ListenAddrs = endpoints
	}

	return &cfg, nil
}

type CLIFlags struct {
	ConfigFile     string
	Mode           string
	Verbose        bool
	ListenAddr     string
	Servers        string
	ListenAddrs    string
	TargetAddr     string
	DedupWindow    int
	SessionTimeout time.Duration
}

func ParseCLIFlags(args []string) (*CLIFlags, error) {
	fs := flag.NewFlagSet("fproxy", flag.ContinueOnError)

	flags := &CLIFlags{}
	fs.StringVar(&flags.ConfigFile, "config", "", "Path to YAML config file")
	fs.StringVar(&flags.Mode, "mode", "", "Mode: 'client' or 'server'")
	fs.BoolVar(&flags.Verbose, "verbose", false, "Enable debug-level logging")
	fs.StringVar(&flags.ListenAddr, "listen", "", "Client: UDP address to listen on (e.g., :5000)")
	fs.StringVar(&flags.Servers, "servers", "", "Client: Server endpoints (e.g., server:8001/udp,server:8002/tcp)")
	fs.StringVar(&flags.ListenAddrs, "listen-addrs", "", "Server: Listen addresses (e.g., :8001/udp,:8002/tcp)")
	fs.StringVar(&flags.TargetAddr, "target", "", "Server: Target UDP address (e.g., target:9000)")
	fs.IntVar(&flags.DedupWindow, "dedup-window", 0, "Server: Deduplication window size (default: 10000)")
	fs.DurationVar(&flags.SessionTimeout, "session-timeout", 0, "Session timeout duration (default: 60s)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	return flags, nil
}

func Load(args []string) (*Config, error) {
	flags, err := ParseCLIFlags(args)
	if err != nil {
		return nil, err
	}

	var cfg *Config

	// Load from config file if specified
	if flags.ConfigFile != "" {
		cfg, err = LoadFromFile(flags.ConfigFile)
		if err != nil {
			return nil, err
		}
	} else {
		cfg = &Config{}
	}

	// Override with CLI flags
	if flags.Mode != "" {
		cfg.Mode = Mode(flags.Mode)
	}
	if flags.Verbose {
		cfg.Verbose = true
	}

	// Client config overrides
	if flags.ListenAddr != "" || flags.Servers != "" || flags.SessionTimeout > 0 {
		if cfg.Client == nil {
			cfg.Client = &ClientConfig{}
		}
		if flags.ListenAddr != "" {
			cfg.Client.ListenAddr = flags.ListenAddr
		}
		if flags.Servers != "" {
			endpoints, err := ParseEndpoints(flags.Servers)
			if err != nil {
				return nil, fmt.Errorf("invalid servers flag: %w", err)
			}
			cfg.Client.Servers = endpoints
		}
		if flags.SessionTimeout > 0 {
			cfg.Client.SessionTimeout = flags.SessionTimeout
		}
	}

	// Server config overrides
	if flags.ListenAddrs != "" || flags.TargetAddr != "" || flags.DedupWindow > 0 || flags.SessionTimeout > 0 {
		if cfg.Server == nil {
			cfg.Server = &ServerConfig{}
		}
		if flags.ListenAddrs != "" {
			endpoints, err := ParseEndpoints(flags.ListenAddrs)
			if err != nil {
				return nil, fmt.Errorf("invalid listen-addrs flag: %w", err)
			}
			cfg.Server.ListenAddrs = endpoints
		}
		if flags.TargetAddr != "" {
			cfg.Server.TargetAddr = flags.TargetAddr
		}
		if flags.DedupWindow > 0 {
			cfg.Server.DedupWindow = flags.DedupWindow
		}
		if flags.SessionTimeout > 0 {
			cfg.Server.SessionTimeout = flags.SessionTimeout
		}
	}

	// Set default dedup window if server mode
	if cfg.Server != nil && cfg.Server.DedupWindow <= 0 {
		cfg.Server.DedupWindow = DefaultDedupWindow
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
