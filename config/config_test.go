package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Endpoint
		wantErr bool
	}{
		{
			name:  "valid UDP endpoint with IPv4",
			input: "192.168.1.1:8001/udp",
			want:  Endpoint{Address: "192.168.1.1:8001", Protocol: "udp"},
		},
		{
			name:  "valid TCP endpoint with IPv4",
			input: "10.0.0.1:8002/tcp",
			want:  Endpoint{Address: "10.0.0.1:8002", Protocol: "tcp"},
		},
		{
			name:  "valid endpoint with IPv6",
			input: "[::1]:8001/udp",
			want:  Endpoint{Address: "[::1]:8001", Protocol: "udp"},
		},
		{
			name:  "uppercase protocol",
			input: "127.0.0.1:1234/UDP",
			want:  Endpoint{Address: "127.0.0.1:1234", Protocol: "udp"},
		},
		{
			name:  "listen address with empty host",
			input: ":8001/udp",
			want:  Endpoint{Address: ":8001", Protocol: "udp"},
		},
		{
			name:    "missing protocol",
			input:   "192.168.1.1:8001",
			wantErr: true,
		},
		{
			name:    "invalid protocol",
			input:   "192.168.1.1:8001/http",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "domain name not allowed",
			input:   "localhost:8001/udp",
			wantErr: true,
		},
		{
			name:    "hostname not allowed",
			input:   "myserver.example.com:8001/tcp",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEndpoint(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEndpoint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Endpoint
		wantErr bool
	}{
		{
			name:  "single endpoint",
			input: "192.168.1.1:8001/udp",
			want:  []Endpoint{{Address: "192.168.1.1:8001", Protocol: "udp"}},
		},
		{
			name:  "multiple endpoints same IP",
			input: "192.168.1.1:8001/udp,192.168.1.1:8002/tcp",
			want: []Endpoint{
				{Address: "192.168.1.1:8001", Protocol: "udp"},
				{Address: "192.168.1.1:8002", Protocol: "tcp"},
			},
		},
		{
			name:  "with spaces",
			input: "10.0.0.1:8001/udp, 10.0.0.1:8002/tcp",
			want: []Endpoint{
				{Address: "10.0.0.1:8001", Protocol: "udp"},
				{Address: "10.0.0.1:8002", Protocol: "tcp"},
			},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:    "invalid endpoint in list",
			input:   "192.168.1.1:8001/udp,invalid",
			wantErr: true,
		},
		{
			name:    "domain name in list not allowed",
			input:   "server:8001/udp,server:8002/tcp",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEndpoints(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEndpoints() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("ParseEndpoints() len = %d, want %d", len(got), len(tt.want))
					return
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("ParseEndpoints()[%d] = %v, want %v", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{
			name:    "missing mode",
			cfg:     Config{},
			wantErr: ErrModeRequired,
		},
		{
			name:    "invalid mode",
			cfg:     Config{Mode: "invalid"},
			wantErr: ErrInvalidMode,
		},
		{
			name: "client missing listen",
			cfg: Config{
				Mode:   ModeClient,
				Client: &ClientConfig{Servers: []Endpoint{{Address: "192.168.1.1:8001", Protocol: "udp"}}},
			},
			wantErr: ErrClientListenRequired,
		},
		{
			name: "client missing servers",
			cfg: Config{
				Mode:   ModeClient,
				Client: &ClientConfig{ListenAddr: ":5000"},
			},
			wantErr: ErrClientServersRequired,
		},
		{
			name: "valid client config with single server",
			cfg: Config{
				Mode: ModeClient,
				Client: &ClientConfig{
					ListenAddr: ":5000",
					Servers:    []Endpoint{{Address: "192.168.1.1:8001", Protocol: "udp"}},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid client config with multiple servers same IP",
			cfg: Config{
				Mode: ModeClient,
				Client: &ClientConfig{
					ListenAddr: ":5000",
					Servers: []Endpoint{
						{Address: "192.168.1.1:8001", Protocol: "udp"},
						{Address: "192.168.1.1:8002", Protocol: "tcp"},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "client servers with different IPs",
			cfg: Config{
				Mode: ModeClient,
				Client: &ClientConfig{
					ListenAddr: ":5000",
					Servers: []Endpoint{
						{Address: "192.168.1.1:8001", Protocol: "udp"},
						{Address: "192.168.1.2:8001", Protocol: "tcp"},
					},
				},
			},
			wantErr: ErrClientServersDifferentIPs,
		},
		{
			name: "valid client config with equivalent IPv6 addresses",
			cfg: Config{
				Mode: ModeClient,
				Client: &ClientConfig{
					ListenAddr: ":5000",
					Servers: []Endpoint{
						{Address: "[::1]:8001", Protocol: "udp"},
						{Address: "[0:0:0:0:0:0:0:1]:8002", Protocol: "tcp"},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid client config with IPv6 different ports",
			cfg: Config{
				Mode: ModeClient,
				Client: &ClientConfig{
					ListenAddr: ":5000",
					Servers: []Endpoint{
						{Address: "[2001:db8::1]:8001", Protocol: "udp"},
						{Address: "[2001:db8::1]:8002", Protocol: "tcp"},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "server missing listen addrs",
			cfg: Config{
				Mode:   ModeServer,
				Server: &ServerConfig{TargetAddr: "192.168.1.100:9000"},
			},
			wantErr: ErrServerListenRequired,
		},
		{
			name: "server missing target",
			cfg: Config{
				Mode:   ModeServer,
				Server: &ServerConfig{ListenAddrs: []Endpoint{{Address: ":8001", Protocol: "udp"}}},
			},
			wantErr: ErrServerTargetRequired,
		},
		{
			name: "valid server config",
			cfg: Config{
				Mode: ModeServer,
				Server: &ServerConfig{
					ListenAddrs: []Endpoint{{Address: ":8001", Protocol: "udp"}},
					TargetAddr:  "192.168.1.100:9000",
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test config file
	clientYAML := `
mode: client
client:
  listen_addr: ":5000"
  servers: "192.168.1.1:8001/udp,192.168.1.1:8002/tcp"
`
	clientPath := filepath.Join(tmpDir, "client.yaml")
	if err := os.WriteFile(clientPath, []byte(clientYAML), 0644); err != nil {
		t.Fatal(err)
	}

	serverYAML := `
mode: server
server:
  listen_addrs: ":8001/udp,:8002/tcp"
  target_addr: "192.168.1.100:9000"
  dedup_window: 5000
`
	serverPath := filepath.Join(tmpDir, "server.yaml")
	if err := os.WriteFile(serverPath, []byte(serverYAML), 0644); err != nil {
		t.Fatal(err)
	}

	invalidClientYAML := `
mode: client
client:
  listen_addr: ":5000"
  servers: "server:8001/udp,server:8002/tcp"
`
	invalidClientPath := filepath.Join(tmpDir, "invalid_client.yaml")
	if err := os.WriteFile(invalidClientPath, []byte(invalidClientYAML), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("load client config", func(t *testing.T) {
		cfg, err := LoadFromFile(clientPath)
		if err != nil {
			t.Fatalf("LoadFromFile() error = %v", err)
		}
		if cfg.Mode != ModeClient {
			t.Errorf("Mode = %v, want %v", cfg.Mode, ModeClient)
		}
		if cfg.Client.ListenAddr != ":5000" {
			t.Errorf("ListenAddr = %v, want :5000", cfg.Client.ListenAddr)
		}
		if len(cfg.Client.Servers) != 2 {
			t.Errorf("Servers len = %d, want 2", len(cfg.Client.Servers))
		}
	})

	t.Run("load server config", func(t *testing.T) {
		cfg, err := LoadFromFile(serverPath)
		if err != nil {
			t.Fatalf("LoadFromFile() error = %v", err)
		}
		if cfg.Mode != ModeServer {
			t.Errorf("Mode = %v, want %v", cfg.Mode, ModeServer)
		}
		if cfg.Server.TargetAddr != "192.168.1.100:9000" {
			t.Errorf("TargetAddr = %v, want 192.168.1.100:9000", cfg.Server.TargetAddr)
		}
		if cfg.Server.DedupWindow != 5000 {
			t.Errorf("DedupWindow = %d, want 5000", cfg.Server.DedupWindow)
		}
		if len(cfg.Server.ListenAddrs) != 2 {
			t.Errorf("ListenAddrs len = %d, want 2", len(cfg.Server.ListenAddrs))
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := LoadFromFile("/nonexistent/path.yaml")
		if err == nil {
			t.Error("LoadFromFile() expected error for nonexistent file")
		}
	})

	t.Run("domain name in config file rejected", func(t *testing.T) {
		_, err := LoadFromFile(invalidClientPath)
		if err == nil {
			t.Error("LoadFromFile() expected error for domain name in servers")
		}
	})
}

func TestParseCLIFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want *CLIFlags
	}{
		{
			name: "client flags",
			args: []string{"-mode", "client", "-listen", ":5000", "-servers", "192.168.1.1:8001/udp"},
			want: &CLIFlags{
				Mode:       "client",
				ListenAddr: ":5000",
				Servers:    "192.168.1.1:8001/udp",
			},
		},
		{
			name: "server flags",
			args: []string{"-mode", "server", "-listen-addrs", ":8001/udp", "-target", "192.168.1.100:9000", "-dedup-window", "5000"},
			want: &CLIFlags{
				Mode:        "server",
				ListenAddrs: ":8001/udp",
				TargetAddr:  "192.168.1.100:9000",
				DedupWindow: 5000,
			},
		},
		{
			name: "config file flag",
			args: []string{"-config", "/path/to/config.yaml"},
			want: &CLIFlags{
				ConfigFile: "/path/to/config.yaml",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCLIFlags(tt.args)
			if err != nil {
				t.Fatalf("ParseCLIFlags() error = %v", err)
			}
			if tt.want.Mode != "" && got.Mode != tt.want.Mode {
				t.Errorf("Mode = %v, want %v", got.Mode, tt.want.Mode)
			}
			if tt.want.ListenAddr != "" && got.ListenAddr != tt.want.ListenAddr {
				t.Errorf("ListenAddr = %v, want %v", got.ListenAddr, tt.want.ListenAddr)
			}
			if tt.want.Servers != "" && got.Servers != tt.want.Servers {
				t.Errorf("Servers = %v, want %v", got.Servers, tt.want.Servers)
			}
			if tt.want.ListenAddrs != "" && got.ListenAddrs != tt.want.ListenAddrs {
				t.Errorf("ListenAddrs = %v, want %v", got.ListenAddrs, tt.want.ListenAddrs)
			}
			if tt.want.TargetAddr != "" && got.TargetAddr != tt.want.TargetAddr {
				t.Errorf("TargetAddr = %v, want %v", got.TargetAddr, tt.want.TargetAddr)
			}
			if tt.want.DedupWindow != 0 && got.DedupWindow != tt.want.DedupWindow {
				t.Errorf("DedupWindow = %v, want %v", got.DedupWindow, tt.want.DedupWindow)
			}
			if tt.want.ConfigFile != "" && got.ConfigFile != tt.want.ConfigFile {
				t.Errorf("ConfigFile = %v, want %v", got.ConfigFile, tt.want.ConfigFile)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Run("CLI only client", func(t *testing.T) {
		args := []string{"-mode", "client", "-listen", ":5000", "-servers", "192.168.1.1:8001/udp"}
		cfg, err := Load(args)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Mode != ModeClient {
			t.Errorf("Mode = %v, want client", cfg.Mode)
		}
		if cfg.Client.ListenAddr != ":5000" {
			t.Errorf("ListenAddr = %v, want :5000", cfg.Client.ListenAddr)
		}
	})

	t.Run("CLI only server with default dedup window", func(t *testing.T) {
		args := []string{"-mode", "server", "-listen-addrs", ":8001/udp", "-target", "192.168.1.100:9000"}
		cfg, err := Load(args)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Mode != ModeServer {
			t.Errorf("Mode = %v, want server", cfg.Mode)
		}
		if cfg.Server.DedupWindow != DefaultDedupWindow {
			t.Errorf("DedupWindow = %d, want %d", cfg.Server.DedupWindow, DefaultDedupWindow)
		}
	})

	t.Run("CLI overrides config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configYAML := `
mode: client
client:
  listen_addr: ":5000"
  servers: "192.168.1.1:8001/udp"
`
		configPath := filepath.Join(tmpDir, "config.yaml")
		if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
			t.Fatal(err)
		}

		// Override listen address via CLI
		args := []string{"-config", configPath, "-listen", ":6000"}
		cfg, err := Load(args)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Client.ListenAddr != ":6000" {
			t.Errorf("ListenAddr = %v, want :6000 (CLI override)", cfg.Client.ListenAddr)
		}
	})

	t.Run("CLI with domain name rejected", func(t *testing.T) {
		args := []string{"-mode", "client", "-listen", ":5000", "-servers", "server:8001/udp"}
		_, err := Load(args)
		if err == nil {
			t.Error("Load() expected error for domain name in servers")
		}
	})

	t.Run("CLI with different IPs rejected", func(t *testing.T) {
		args := []string{"-mode", "client", "-listen", ":5000", "-servers", "192.168.1.1:8001/udp,192.168.1.2:8002/tcp"}
		_, err := Load(args)
		if err == nil {
			t.Error("Load() expected error for different IPs in servers")
		}
	})
}
