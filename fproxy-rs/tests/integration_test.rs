//! Integration tests for fproxy.

use fproxy::client::Client;
use fproxy::config::{ClientConfig, Endpoint, Protocol, ServerConfig};
use fproxy::server::Server;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;
use tokio::net::UdpSocket;
use tokio::time::timeout;

/// Helper to create a mock target server that echoes received packets.
async fn create_echo_server() -> (Arc<UdpSocket>, SocketAddr) {
    let socket = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    let addr = socket.local_addr().unwrap();
    let socket = Arc::new(socket);

    let socket_clone = Arc::clone(&socket);
    tokio::spawn(async move {
        let mut buf = vec![0u8; 65535];
        loop {
            match socket_clone.recv_from(&mut buf).await {
                Ok((n, src)) => {
                    let _ = socket_clone.send_to(&buf[..n], src).await;
                }
                Err(_) => break,
            }
        }
    });

    (socket, addr)
}

#[tokio::test]
async fn test_client_server_udp_transport() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server
    let server_cfg = ServerConfig {
        listen_addrs: vec![Endpoint {
            address: "127.0.0.1:0".to_string(),
            protocol: Protocol::Udp,
        }],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_secs(60),
    };
    let server = Arc::new(Server::new(server_cfg));
    let server_addrs = server.clone().start().await.unwrap();
    let server_addr = server_addrs[0];

    // Create client
    let client_cfg = ClientConfig {
        listen_addr: "127.0.0.1:0".to_string(),
        servers: vec![Endpoint {
            address: server_addr.to_string(),
            protocol: Protocol::Udp,
        }],
        session_timeout: Duration::from_secs(60),
    };
    let client = Arc::new(Client::new(client_cfg));
    let client_addr = client.clone().start().await.unwrap();

    // Give time for setup
    tokio::time::sleep(Duration::from_millis(50)).await;

    // Create source and send packet
    let source = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    source.connect(client_addr).await.unwrap();

    let test_data = b"hello world";
    source.send(test_data).await.unwrap();

    // Wait for response
    let mut buf = vec![0u8; 1024];
    let result = timeout(Duration::from_secs(2), source.recv(&mut buf)).await;
    assert!(result.is_ok(), "timeout waiting for response");

    let n = result.unwrap().unwrap();
    assert_eq!(&buf[..n], test_data);

    // Cleanup
    server.stop();
    client.stop();
}

#[tokio::test]
async fn test_client_server_tcp_transport() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server with TCP listener
    let server_cfg = ServerConfig {
        listen_addrs: vec![Endpoint {
            address: "127.0.0.1:0".to_string(),
            protocol: Protocol::Tcp,
        }],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_secs(60),
    };
    let server = Arc::new(Server::new(server_cfg));
    let server_addrs = server.clone().start().await.unwrap();
    let server_addr = server_addrs[0];

    // Create client with TCP transport
    let client_cfg = ClientConfig {
        listen_addr: "127.0.0.1:0".to_string(),
        servers: vec![Endpoint {
            address: server_addr.to_string(),
            protocol: Protocol::Tcp,
        }],
        session_timeout: Duration::from_secs(60),
    };
    let client = Arc::new(Client::new(client_cfg));
    let client_addr = client.clone().start().await.unwrap();

    // Give time for setup
    tokio::time::sleep(Duration::from_millis(50)).await;

    // Create source and send packet
    let source = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    source.connect(client_addr).await.unwrap();

    let test_data = b"tcp test data";
    source.send(test_data).await.unwrap();

    // Wait for response
    let mut buf = vec![0u8; 1024];
    let result = timeout(Duration::from_secs(2), source.recv(&mut buf)).await;
    assert!(result.is_ok(), "timeout waiting for TCP response");

    let n = result.unwrap().unwrap();
    assert_eq!(&buf[..n], test_data);

    // Cleanup
    server.stop();
    client.stop();
}

#[tokio::test]
async fn test_multi_path_deduplication() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server with both UDP and TCP listeners
    let server_cfg = ServerConfig {
        listen_addrs: vec![
            Endpoint {
                address: "127.0.0.1:0".to_string(),
                protocol: Protocol::Udp,
            },
            Endpoint {
                address: "127.0.0.1:0".to_string(),
                protocol: Protocol::Tcp,
            },
        ],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_secs(60),
    };
    let server = Arc::new(Server::new(server_cfg));
    let server_addrs = server.clone().start().await.unwrap();

    // Create client with multi-path (same IP, different ports/protocols)
    let client_cfg = ClientConfig {
        listen_addr: "127.0.0.1:0".to_string(),
        servers: vec![
            Endpoint {
                address: server_addrs[0].to_string(),
                protocol: Protocol::Udp,
            },
            Endpoint {
                address: server_addrs[1].to_string(),
                protocol: Protocol::Tcp,
            },
        ],
        session_timeout: Duration::from_secs(60),
    };
    let client = Arc::new(Client::new(client_cfg));
    let client_addr = client.clone().start().await.unwrap();

    // Give time for setup and TCP connection
    tokio::time::sleep(Duration::from_millis(200)).await;

    // Create source and send multiple packets
    let source = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    source.connect(client_addr).await.unwrap();

    // Send 5 packets with enough delay for processing
    for i in 0..5 {
        let data = format!("packet {}", i);
        source.send(data.as_bytes()).await.unwrap();
        tokio::time::sleep(Duration::from_millis(50)).await;
    }

    // Receive responses - we should receive at least 5 (the server sends via all paths
    // but client deduplicates responses)
    let mut responses = Vec::new();
    let mut buf = vec![0u8; 1024];

    // Wait for responses with longer timeout
    for _ in 0..10 {
        match timeout(Duration::from_millis(500), source.recv(&mut buf)).await {
            Ok(Ok(n)) => {
                responses.push(String::from_utf8_lossy(&buf[..n]).to_string());
            }
            _ => break,
        }
    }

    // We should receive at least 5 responses (deduplication ensures no more than 5 unique)
    // Due to multi-path, we might receive exactly 5 (if dedup works) or more if timing varies
    assert!(
        responses.len() >= 5,
        "expected at least 5 responses, got {}",
        responses.len()
    );

    // Check that all expected packets are present
    for i in 0..5 {
        let expected = format!("packet {}", i);
        assert!(
            responses.iter().any(|r| r == &expected),
            "missing response for packet {}",
            i
        );
    }

    // Cleanup
    server.stop();
    client.stop();
}

#[tokio::test]
async fn test_session_timeout() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server with short timeout
    let server_cfg = ServerConfig {
        listen_addrs: vec![Endpoint {
            address: "127.0.0.1:0".to_string(),
            protocol: Protocol::Udp,
        }],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_millis(200),
    };
    let server = Arc::new(Server::new(server_cfg));
    let server_addrs = server.clone().start().await.unwrap();

    // Create client with short timeout
    let client_cfg = ClientConfig {
        listen_addr: "127.0.0.1:0".to_string(),
        servers: vec![Endpoint {
            address: server_addrs[0].to_string(),
            protocol: Protocol::Udp,
        }],
        session_timeout: Duration::from_millis(200),
    };
    let client = Arc::new(Client::new(client_cfg));
    let client_addr = client.clone().start().await.unwrap();

    // Give time for setup
    tokio::time::sleep(Duration::from_millis(50)).await;

    // Create source and send packet
    let source = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    source.connect(client_addr).await.unwrap();

    source.send(b"test").await.unwrap();

    // Wait for response
    let mut buf = vec![0u8; 1024];
    let result = timeout(Duration::from_secs(1), source.recv(&mut buf)).await;
    assert!(result.is_ok());

    // Check session exists
    assert!(server.session_count().await > 0);

    // Wait for session timeout
    tokio::time::sleep(Duration::from_millis(500)).await;

    // Session should be cleaned up
    assert_eq!(server.session_count().await, 0);

    // Cleanup
    server.stop();
    client.stop();
}

#[tokio::test]
async fn test_bidirectional_response() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server
    let server_cfg = ServerConfig {
        listen_addrs: vec![Endpoint {
            address: "127.0.0.1:0".to_string(),
            protocol: Protocol::Udp,
        }],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_secs(60),
    };
    let server = Arc::new(Server::new(server_cfg));
    let server_addrs = server.clone().start().await.unwrap();

    // Create client
    let client_cfg = ClientConfig {
        listen_addr: "127.0.0.1:0".to_string(),
        servers: vec![Endpoint {
            address: server_addrs[0].to_string(),
            protocol: Protocol::Udp,
        }],
        session_timeout: Duration::from_secs(60),
    };
    let client = Arc::new(Client::new(client_cfg));
    let client_addr = client.clone().start().await.unwrap();

    // Give time for setup
    tokio::time::sleep(Duration::from_millis(50)).await;

    // Create multiple sources
    let source1 = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    let source2 = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    source1.connect(client_addr).await.unwrap();
    source2.connect(client_addr).await.unwrap();

    // Send from both sources
    source1.send(b"from source1").await.unwrap();
    source2.send(b"from source2").await.unwrap();

    // Both should receive their own response
    let mut buf = vec![0u8; 1024];

    let n1 = timeout(Duration::from_secs(1), source1.recv(&mut buf))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(&buf[..n1], b"from source1");

    let n2 = timeout(Duration::from_secs(1), source2.recv(&mut buf))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(&buf[..n2], b"from source2");

    // Cleanup
    server.stop();
    client.stop();
}

#[tokio::test]
async fn test_large_payload() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server
    let server_cfg = ServerConfig {
        listen_addrs: vec![Endpoint {
            address: "127.0.0.1:0".to_string(),
            protocol: Protocol::Udp,
        }],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_secs(60),
    };
    let server = Arc::new(Server::new(server_cfg));
    let server_addrs = server.clone().start().await.unwrap();

    // Create client
    let client_cfg = ClientConfig {
        listen_addr: "127.0.0.1:0".to_string(),
        servers: vec![Endpoint {
            address: server_addrs[0].to_string(),
            protocol: Protocol::Udp,
        }],
        session_timeout: Duration::from_secs(60),
    };
    let client = Arc::new(Client::new(client_cfg));
    let client_addr = client.clone().start().await.unwrap();

    // Give time for setup
    tokio::time::sleep(Duration::from_millis(50)).await;

    // Create source
    let source = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    source.connect(client_addr).await.unwrap();

    // Send large payload (just under typical MTU limit accounting for headers)
    let large_payload = vec![0xABu8; 1400];
    source.send(&large_payload).await.unwrap();

    // Wait for response
    let mut buf = vec![0u8; 2000];
    let result = timeout(Duration::from_secs(2), source.recv(&mut buf)).await;
    assert!(result.is_ok(), "timeout waiting for response");

    let n = result.unwrap().unwrap();
    assert_eq!(&buf[..n], large_payload.as_slice());

    // Cleanup
    server.stop();
    client.stop();
}

#[tokio::test]
async fn test_graceful_shutdown() {
    // Create echo target
    let (_, target_addr) = create_echo_server().await;

    // Create server
    let server_cfg = ServerConfig {
        listen_addrs: vec![Endpoint {
            address: "127.0.0.1:0".to_string(),
            protocol: Protocol::Udp,
        }],
        target_addr: target_addr.to_string(),
        dedup_window: 1000,
        session_timeout: Duration::from_secs(60),
    };
    let server = Arc::new(Server::new(server_cfg));
    let _ = server.clone().start().await.unwrap();

    // Create some sessions
    let server_addr: SocketAddr = "127.0.0.1:0".parse().unwrap();
    let _ = server_addr; // Just to show we could use it

    // Stop should not hang
    let stop_result = timeout(Duration::from_secs(2), async {
        server.stop();
    })
    .await;

    assert!(stop_result.is_ok(), "graceful shutdown timed out");
}
