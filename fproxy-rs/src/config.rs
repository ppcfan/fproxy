//! Configuration loading and validation for fproxy.
//!
//! Supports both CLI flags and YAML configuration files.
//! CLI flags take precedence over config file values.

use clap::Parser;
use serde::Deserialize;
use std::net::IpAddr;
use std::path::PathBuf;
use std::time::Duration;
use thiserror::Error;

/// Default deduplication window size.
pub const DEFAULT_DEDUP_WINDOW: usize = 10000;

/// Default session timeout in seconds.
pub const DEFAULT_SESSION_TIMEOUT_SECS: u64 = 60;

/// Configuration errors.
#[derive(Error, Debug, PartialEq)]
pub enum ConfigError {
    #[error("mode is required: must be 'client' or 'server'")]
    ModeRequired,

    #[error("invalid mode: must be 'client' or 'server'")]
    InvalidMode,

    #[error("client listen address is required")]
    ClientListenRequired,

    #[error("client server endpoints are required")]
    ClientServersRequired,

    #[error("server listen addresses are required")]
    ServerListenRequired,

    #[error("server target address is required")]
    ServerTargetRequired,

    #[error("invalid endpoint format {0:?}: expected address/protocol")]
    InvalidEndpointFormat(String),

    #[error("invalid protocol {0:?}: must be 'udp' or 'tcp'")]
    InvalidProtocol(String),

    #[error("invalid address format {0:?}: {1}")]
    InvalidAddressFormat(String, String),

    #[error("server address must be an IP address, not a domain name")]
    InvalidIPAddress,

    #[error("all server endpoints must have the same IP address: deduplication cannot work across separate servers")]
    ClientServersDifferentIPs,

    #[error("failed to read config file: {0}")]
    FileReadError(String),

    #[error("failed to parse config file: {0}")]
    FileParseError(String),
}

/// Operating mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Mode {
    #[default]
    Client,
    Server,
}

impl std::str::FromStr for Mode {
    type Err = ();

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s.to_lowercase().as_str() {
            "client" => Ok(Mode::Client),
            "server" => Ok(Mode::Server),
            _ => Err(()),
        }
    }
}

/// Server endpoint with address and protocol.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Endpoint {
    pub address: String,
    pub protocol: Protocol,
}

/// Transport protocol.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Protocol {
    Udp,
    Tcp,
}

impl Endpoint {
    /// Parses an endpoint string in the format "address/protocol".
    ///
    /// Example: "192.168.1.1:8001/udp" or "[::1]:8001/tcp"
    pub fn parse(s: &str) -> Result<Endpoint, ConfigError> {
        let parts: Vec<&str> = s.split('/').collect();
        if parts.len() != 2 {
            return Err(ConfigError::InvalidEndpointFormat(s.to_string()));
        }

        let address = parts[0].to_string();
        let protocol = match parts[1].to_lowercase().as_str() {
            "udp" => Protocol::Udp,
            "tcp" => Protocol::Tcp,
            _ => return Err(ConfigError::InvalidProtocol(parts[1].to_string())),
        };

        // Validate address format and extract host
        let host = extract_host(&address)?;

        // Allow empty host (e.g., ":8001" for listen addresses)
        if !host.is_empty() && !is_valid_ip(&host) {
            return Err(ConfigError::InvalidIPAddress);
        }

        // Normalize empty host to 0.0.0.0 for listen addresses
        let address = normalize_listen_addr(&address);

        Ok(Endpoint { address, protocol })
    }

    /// Parses a comma-separated list of endpoints.
    pub fn parse_list(s: &str) -> Result<Vec<Endpoint>, ConfigError> {
        if s.is_empty() {
            return Ok(Vec::new());
        }

        s.split(',')
            .map(|p| p.trim())
            .filter(|p| !p.is_empty())
            .map(Endpoint::parse)
            .collect()
    }
}

/// Extracts the host part from an address in "host:port" or "[host]:port" format.
fn extract_host(address: &str) -> Result<String, ConfigError> {
    // Handle IPv6 addresses in brackets
    if address.starts_with('[') {
        if let Some(bracket_end) = address.find(']') {
            return Ok(address[1..bracket_end].to_string());
        }
        return Err(ConfigError::InvalidAddressFormat(
            address.to_string(),
            "unclosed bracket".to_string(),
        ));
    }

    // Find the last colon (for port)
    if let Some(colon_pos) = address.rfind(':') {
        Ok(address[..colon_pos].to_string())
    } else {
        Err(ConfigError::InvalidAddressFormat(
            address.to_string(),
            "missing port".to_string(),
        ))
    }
}

/// Checks if a string is a valid IP address.
fn is_valid_ip(host: &str) -> bool {
    host.parse::<IpAddr>().is_ok()
}

/// Parses host IP from an address, returning None for empty host.
fn parse_host_ip(address: &str) -> Result<Option<IpAddr>, ConfigError> {
    let host = extract_host(address)?;
    if host.is_empty() {
        return Ok(None);
    }
    host.parse::<IpAddr>()
        .map(Some)
        .map_err(|_| ConfigError::InvalidIPAddress)
}

/// Client mode configuration.
#[derive(Debug, Clone, Default)]
pub struct ClientConfig {
    pub listen_addr: String,
    pub servers: Vec<Endpoint>,
    pub session_timeout: Duration,
}

/// Server mode configuration.
#[derive(Debug, Clone, Default)]
pub struct ServerConfig {
    pub listen_addrs: Vec<Endpoint>,
    pub target_addr: String,
    pub dedup_window: usize,
    pub session_timeout: Duration,
}

/// Main configuration structure.
#[derive(Debug, Clone)]
pub struct Config {
    pub mode: Mode,
    pub verbose: bool,
    pub client: Option<ClientConfig>,
    pub server: Option<ServerConfig>,
}

impl Config {
    /// Validates the configuration.
    pub fn validate(&mut self) -> Result<(), ConfigError> {
        match self.mode {
            Mode::Client => {
                let client = self.client.get_or_insert_with(ClientConfig::default);

                if client.listen_addr.is_empty() {
                    return Err(ConfigError::ClientListenRequired);
                }
                if client.servers.is_empty() {
                    return Err(ConfigError::ClientServersRequired);
                }

                // Validate all server endpoints have the same IP address
                if client.servers.len() > 1 {
                    let first_ip = parse_host_ip(&client.servers[0].address)?;
                    for endpoint in &client.servers[1..] {
                        let ip = parse_host_ip(&endpoint.address)?;
                        if first_ip != ip {
                            return Err(ConfigError::ClientServersDifferentIPs);
                        }
                    }
                }

                if client.session_timeout.is_zero() {
                    client.session_timeout = Duration::from_secs(DEFAULT_SESSION_TIMEOUT_SECS);
                }
            }
            Mode::Server => {
                let server = self.server.get_or_insert_with(ServerConfig::default);

                if server.listen_addrs.is_empty() {
                    return Err(ConfigError::ServerListenRequired);
                }
                if server.target_addr.is_empty() {
                    return Err(ConfigError::ServerTargetRequired);
                }
                if server.dedup_window == 0 {
                    server.dedup_window = DEFAULT_DEDUP_WINDOW;
                }
                if server.session_timeout.is_zero() {
                    server.session_timeout = Duration::from_secs(DEFAULT_SESSION_TIMEOUT_SECS);
                }
            }
        }

        Ok(())
    }
}

/// YAML configuration file structure.
#[derive(Debug, Deserialize, Default)]
struct YamlConfig {
    mode: Option<String>,
    verbose: Option<bool>,
    client: Option<YamlClientConfig>,
    server: Option<YamlServerConfig>,
}

#[derive(Debug, Deserialize, Default)]
struct YamlClientConfig {
    listen_addr: Option<String>,
    servers: Option<String>,
    session_timeout: Option<String>,
}

#[derive(Debug, Deserialize, Default)]
struct YamlServerConfig {
    listen_addrs: Option<String>,
    target_addr: Option<String>,
    dedup_window: Option<usize>,
    session_timeout: Option<String>,
}

/// CLI argument parser.
#[derive(Parser, Debug)]
#[command(name = "fproxy")]
#[command(about = "A Layer 4 bidirectional UDP relay with multi-path transmission")]
pub struct CliArgs {
    /// Path to YAML config file
    #[arg(long)]
    pub config: Option<PathBuf>,

    /// Mode: 'client' or 'server'
    #[arg(long)]
    pub mode: Option<String>,

    /// Enable debug-level logging
    #[arg(long)]
    pub verbose: bool,

    /// Client: UDP address to listen on (e.g., :5000)
    #[arg(long)]
    pub listen: Option<String>,

    /// Client: Server endpoints (e.g., server:8001/udp,server:8002/tcp)
    #[arg(long)]
    pub servers: Option<String>,

    /// Server: Listen addresses (e.g., :8001/udp,:8002/tcp)
    #[arg(long = "listen-addrs")]
    pub listen_addrs: Option<String>,

    /// Server: Target UDP address (e.g., target:9000)
    #[arg(long)]
    pub target: Option<String>,

    /// Server: Deduplication window size (default: 10000)
    #[arg(long = "dedup-window")]
    pub dedup_window: Option<usize>,

    /// Session timeout duration (e.g., 60s, 5m)
    #[arg(long = "session-timeout")]
    pub session_timeout: Option<String>,
}

/// Normalizes a listen address by replacing empty host with "0.0.0.0".
/// This makes ":4000" become "0.0.0.0:4000" for compatibility with Go-style addresses.
fn normalize_listen_addr(addr: &str) -> String {
    if addr.starts_with(':') {
        format!("0.0.0.0{}", addr)
    } else if addr.starts_with("[:]:") {
        // IPv6 empty host
        format!("[::]{}", &addr[3..])
    } else {
        addr.to_string()
    }
}

/// Parses a duration string like "60s", "5m", "1h".
fn parse_duration(s: &str) -> Option<Duration> {
    let s = s.trim();
    if s.is_empty() {
        return None;
    }

    let (num_str, unit) = if let Some(n) = s.strip_suffix("ms") {
        (n, "ms")
    } else if let Some(n) = s.strip_suffix('s') {
        (n, "s")
    } else if let Some(n) = s.strip_suffix('m') {
        (n, "m")
    } else if let Some(n) = s.strip_suffix('h') {
        (n, "h")
    } else {
        // Default to seconds if no unit
        (s, "s")
    };

    let num: u64 = num_str.parse().ok()?;

    match unit {
        "ms" => Some(Duration::from_millis(num)),
        "s" => Some(Duration::from_secs(num)),
        "m" => Some(Duration::from_secs(num * 60)),
        "h" => Some(Duration::from_secs(num * 3600)),
        _ => None,
    }
}

/// Loads configuration from file.
fn load_from_file(path: &PathBuf) -> Result<YamlConfig, ConfigError> {
    let content =
        std::fs::read_to_string(path).map_err(|e| ConfigError::FileReadError(e.to_string()))?;

    serde_yaml::from_str(&content).map_err(|e| ConfigError::FileParseError(e.to_string()))
}

/// Loads configuration from CLI arguments and optional config file.
pub fn load(args: CliArgs) -> Result<Config, ConfigError> {
    // Load from config file if specified
    let yaml_config = if let Some(ref path) = args.config {
        load_from_file(path)?
    } else {
        YamlConfig::default()
    };

    // Determine mode (CLI takes precedence)
    let mode_str = args.mode.or(yaml_config.mode);
    let mode = match mode_str {
        Some(ref s) => s.parse::<Mode>().map_err(|_| ConfigError::InvalidMode)?,
        None => return Err(ConfigError::ModeRequired),
    };

    // Verbose flag (CLI takes precedence if true)
    let verbose = args.verbose || yaml_config.verbose.unwrap_or(false);

    // Build client config
    let client = if mode == Mode::Client {
        let yaml_client = yaml_config.client.unwrap_or_default();

        let listen_addr = args.listen.or(yaml_client.listen_addr).unwrap_or_default();
        let listen_addr = normalize_listen_addr(&listen_addr);

        let servers_str = args.servers.or(yaml_client.servers).unwrap_or_default();
        let servers = Endpoint::parse_list(&servers_str)?;

        let session_timeout = args
            .session_timeout
            .as_deref()
            .or(yaml_client.session_timeout.as_deref())
            .and_then(parse_duration)
            .unwrap_or(Duration::ZERO);

        Some(ClientConfig {
            listen_addr,
            servers,
            session_timeout,
        })
    } else {
        None
    };

    // Build server config
    let server = if mode == Mode::Server {
        let yaml_server = yaml_config.server.unwrap_or_default();

        let listen_addrs_str = args
            .listen_addrs
            .or(yaml_server.listen_addrs)
            .unwrap_or_default();
        let listen_addrs = Endpoint::parse_list(&listen_addrs_str)?;

        let target_addr = args.target.or(yaml_server.target_addr).unwrap_or_default();

        let dedup_window = args.dedup_window.or(yaml_server.dedup_window).unwrap_or(0);

        let session_timeout = args
            .session_timeout
            .as_deref()
            .or(yaml_server.session_timeout.as_deref())
            .and_then(parse_duration)
            .unwrap_or(Duration::ZERO);

        Some(ServerConfig {
            listen_addrs,
            target_addr,
            dedup_window,
            session_timeout,
        })
    } else {
        None
    };

    let mut config = Config {
        mode,
        verbose,
        client,
        server,
    };

    config.validate()?;
    Ok(config)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_endpoint_udp() {
        let ep = Endpoint::parse("192.168.1.1:8001/udp").unwrap();
        assert_eq!(ep.address, "192.168.1.1:8001");
        assert_eq!(ep.protocol, Protocol::Udp);
    }

    #[test]
    fn test_parse_endpoint_tcp() {
        let ep = Endpoint::parse("192.168.1.1:8002/tcp").unwrap();
        assert_eq!(ep.address, "192.168.1.1:8002");
        assert_eq!(ep.protocol, Protocol::Tcp);
    }

    #[test]
    fn test_parse_endpoint_ipv6() {
        let ep = Endpoint::parse("[::1]:8001/udp").unwrap();
        assert_eq!(ep.address, "[::1]:8001");
        assert_eq!(ep.protocol, Protocol::Udp);
    }

    #[test]
    fn test_parse_endpoint_listen_addr() {
        let ep = Endpoint::parse(":8001/udp").unwrap();
        assert_eq!(ep.address, "0.0.0.0:8001"); // Normalized for Go compatibility
        assert_eq!(ep.protocol, Protocol::Udp);
    }

    #[test]
    fn test_parse_endpoint_invalid_format() {
        assert!(matches!(
            Endpoint::parse("192.168.1.1:8001"),
            Err(ConfigError::InvalidEndpointFormat(_))
        ));
    }

    #[test]
    fn test_parse_endpoint_invalid_protocol() {
        assert!(matches!(
            Endpoint::parse("192.168.1.1:8001/http"),
            Err(ConfigError::InvalidProtocol(_))
        ));
    }

    #[test]
    fn test_parse_endpoint_domain_rejected() {
        assert!(matches!(
            Endpoint::parse("example.com:8001/udp"),
            Err(ConfigError::InvalidIPAddress)
        ));
    }

    #[test]
    fn test_parse_endpoint_list() {
        let endpoints = Endpoint::parse_list("192.168.1.1:8001/udp, 192.168.1.1:8002/tcp").unwrap();
        assert_eq!(endpoints.len(), 2);
        assert_eq!(endpoints[0].protocol, Protocol::Udp);
        assert_eq!(endpoints[1].protocol, Protocol::Tcp);
    }

    #[test]
    fn test_parse_endpoint_list_empty() {
        let endpoints = Endpoint::parse_list("").unwrap();
        assert!(endpoints.is_empty());
    }

    #[test]
    fn test_parse_duration() {
        assert_eq!(parse_duration("60s"), Some(Duration::from_secs(60)));
        assert_eq!(parse_duration("5m"), Some(Duration::from_secs(300)));
        assert_eq!(parse_duration("1h"), Some(Duration::from_secs(3600)));
        assert_eq!(parse_duration("100ms"), Some(Duration::from_millis(100)));
        assert_eq!(parse_duration("60"), Some(Duration::from_secs(60)));
        assert_eq!(parse_duration(""), None);
    }

    #[test]
    fn test_validate_client_missing_listen() {
        let mut config = Config {
            mode: Mode::Client,
            verbose: false,
            client: Some(ClientConfig {
                listen_addr: String::new(),
                servers: vec![Endpoint::parse("192.168.1.1:8001/udp").unwrap()],
                session_timeout: Duration::ZERO,
            }),
            server: None,
        };
        assert_eq!(config.validate(), Err(ConfigError::ClientListenRequired));
    }

    #[test]
    fn test_validate_client_missing_servers() {
        let mut config = Config {
            mode: Mode::Client,
            verbose: false,
            client: Some(ClientConfig {
                listen_addr: ":5000".to_string(),
                servers: vec![],
                session_timeout: Duration::ZERO,
            }),
            server: None,
        };
        assert_eq!(config.validate(), Err(ConfigError::ClientServersRequired));
    }

    #[test]
    fn test_validate_client_different_ips() {
        let mut config = Config {
            mode: Mode::Client,
            verbose: false,
            client: Some(ClientConfig {
                listen_addr: ":5000".to_string(),
                servers: vec![
                    Endpoint::parse("192.168.1.1:8001/udp").unwrap(),
                    Endpoint::parse("192.168.1.2:8002/tcp").unwrap(),
                ],
                session_timeout: Duration::ZERO,
            }),
            server: None,
        };
        assert_eq!(
            config.validate(),
            Err(ConfigError::ClientServersDifferentIPs)
        );
    }

    #[test]
    fn test_validate_client_same_ip() {
        let mut config = Config {
            mode: Mode::Client,
            verbose: false,
            client: Some(ClientConfig {
                listen_addr: ":5000".to_string(),
                servers: vec![
                    Endpoint::parse("192.168.1.1:8001/udp").unwrap(),
                    Endpoint::parse("192.168.1.1:8002/tcp").unwrap(),
                ],
                session_timeout: Duration::ZERO,
            }),
            server: None,
        };
        assert!(config.validate().is_ok());
    }

    #[test]
    fn test_validate_server_missing_listen() {
        let mut config = Config {
            mode: Mode::Server,
            verbose: false,
            client: None,
            server: Some(ServerConfig {
                listen_addrs: vec![],
                target_addr: "192.168.1.100:9000".to_string(),
                dedup_window: 0,
                session_timeout: Duration::ZERO,
            }),
        };
        assert_eq!(config.validate(), Err(ConfigError::ServerListenRequired));
    }

    #[test]
    fn test_validate_server_missing_target() {
        let mut config = Config {
            mode: Mode::Server,
            verbose: false,
            client: None,
            server: Some(ServerConfig {
                listen_addrs: vec![Endpoint::parse(":8001/udp").unwrap()],
                target_addr: String::new(),
                dedup_window: 0,
                session_timeout: Duration::ZERO,
            }),
        };
        assert_eq!(config.validate(), Err(ConfigError::ServerTargetRequired));
    }

    #[test]
    fn test_validate_sets_defaults() {
        let mut config = Config {
            mode: Mode::Server,
            verbose: false,
            client: None,
            server: Some(ServerConfig {
                listen_addrs: vec![Endpoint::parse(":8001/udp").unwrap()],
                target_addr: "192.168.1.100:9000".to_string(),
                dedup_window: 0,
                session_timeout: Duration::ZERO,
            }),
        };
        config.validate().unwrap();

        let server = config.server.unwrap();
        assert_eq!(server.dedup_window, DEFAULT_DEDUP_WINDOW);
        assert_eq!(
            server.session_timeout,
            Duration::from_secs(DEFAULT_SESSION_TIMEOUT_SECS)
        );
    }

    #[test]
    fn test_mode_from_str() {
        assert_eq!("client".parse::<Mode>(), Ok(Mode::Client));
        assert_eq!("CLIENT".parse::<Mode>(), Ok(Mode::Client));
        assert_eq!("server".parse::<Mode>(), Ok(Mode::Server));
        assert_eq!("SERVER".parse::<Mode>(), Ok(Mode::Server));
        assert!("invalid".parse::<Mode>().is_err());
    }
}
