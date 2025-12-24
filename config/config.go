package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

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

func ParseEndpoint(s string) (Endpoint, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return Endpoint{}, fmt.Errorf("invalid endpoint format %q: expected address/protocol", s)
	}
	proto := strings.ToLower(parts[1])
	if proto != "udp" && proto != "tcp" {
		return Endpoint{}, fmt.Errorf("invalid protocol %q: must be 'udp' or 'tcp'", proto)
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
	ListenAddr string     `yaml:"listen_addr"`
	Servers    []Endpoint `yaml:"-"`
	ServersRaw string     `yaml:"servers"`
}

type ServerConfig struct {
	ListenAddrs    []Endpoint `yaml:"-"`
	ListenAddrsRaw string     `yaml:"listen_addrs"`
	TargetAddr     string     `yaml:"target_addr"`
	DedupWindow    int        `yaml:"dedup_window"`
}

type Config struct {
	Mode   Mode          `yaml:"mode"`
	Client *ClientConfig `yaml:"client,omitempty"`
	Server *ServerConfig `yaml:"server,omitempty"`
}

const DefaultDedupWindow = 10000

var (
	ErrModeRequired          = errors.New("mode is required: must be 'client' or 'server'")
	ErrInvalidMode           = errors.New("invalid mode: must be 'client' or 'server'")
	ErrClientListenRequired  = errors.New("client listen address is required")
	ErrClientServersRequired = errors.New("client server endpoints are required")
	ErrServerListenRequired  = errors.New("server listen addresses are required")
	ErrServerTargetRequired  = errors.New("server target address is required")
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
	ConfigFile       string
	Mode             string
	ListenAddr       string
	Servers          string
	ListenAddrs      string
	TargetAddr       string
	DedupWindow      int
}

func ParseCLIFlags(args []string) (*CLIFlags, error) {
	fs := flag.NewFlagSet("fproxy", flag.ContinueOnError)

	flags := &CLIFlags{}
	fs.StringVar(&flags.ConfigFile, "config", "", "Path to YAML config file")
	fs.StringVar(&flags.Mode, "mode", "", "Mode: 'client' or 'server'")
	fs.StringVar(&flags.ListenAddr, "listen", "", "Client: UDP address to listen on (e.g., :5000)")
	fs.StringVar(&flags.Servers, "servers", "", "Client: Server endpoints (e.g., server:8001/udp,server:8002/tcp)")
	fs.StringVar(&flags.ListenAddrs, "listen-addrs", "", "Server: Listen addresses (e.g., :8001/udp,:8002/tcp)")
	fs.StringVar(&flags.TargetAddr, "target", "", "Server: Target UDP address (e.g., target:9000)")
	fs.IntVar(&flags.DedupWindow, "dedup-window", 0, "Server: Deduplication window size (default: 10000)")

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

	// Client config overrides
	if flags.ListenAddr != "" || flags.Servers != "" {
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
	}

	// Server config overrides
	if flags.ListenAddrs != "" || flags.TargetAddr != "" || flags.DedupWindow > 0 {
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
