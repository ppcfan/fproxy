//! Client mode implementation for fproxy.
//!
//! The client listens for UDP packets from sources, tracks sessions by source address,
//! assigns sequence numbers for deduplication, and forwards packets to configured server
//! endpoints. It also receives response packets from servers and forwards them back to
//! the original source address.

use crate::config::{ClientConfig, Protocol};
use crate::protocol;
use std::collections::HashMap;
use std::io;
use std::net::SocketAddr;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::io::{AsyncReadExt, AsyncWriteExt, WriteHalf};
use tokio::net::{TcpStream, UdpSocket};
use tokio::sync::{Mutex, RwLock};
use tokio::time;
use tokio_util::sync::CancellationToken;
use tracing::{debug, error, info, warn};

/// Maximum TCP frame size.
const MAX_TCP_FRAME_SIZE: usize = 65535;

/// Default response dedup window size.
const DEFAULT_RESPONSE_DEDUP_SIZE: usize = 1000;

/// Response deduplication window for tracking seen response sequence numbers.
pub struct ResponseDedupWindow {
    seen: HashMap<u32, ()>,
    min_seq: u32,
    max_seq: u32,
    size: usize,
    started: bool,
}

impl ResponseDedupWindow {
    /// Creates a new response deduplication window.
    pub fn new(size: usize) -> Self {
        Self {
            seen: HashMap::new(),
            min_seq: 0,
            max_seq: 0,
            size,
            started: false,
        }
    }

    /// Returns true if the sequence number has been seen before.
    pub fn is_duplicate(&mut self, seq: u32) -> bool {
        if !self.started {
            self.started = true;
            self.min_seq = seq;
            self.max_seq = seq;
            self.seen.insert(seq, ());
            return false;
        }

        if self.seen.contains_key(&seq) {
            return true;
        }

        if seq < self.min_seq {
            return true; // Too old, treat as duplicate
        }

        self.seen.insert(seq, ());

        if seq > self.max_seq {
            self.max_seq = seq;
            if self.seen.len() > self.size {
                let new_min = self.max_seq.saturating_sub(self.size as u32 - 1);
                self.seen.retain(|&s, _| s >= new_min);
                self.min_seq = new_min;
            }
        }

        false
    }
}

/// UDP session tracks a session for UDP transport.
struct UdpSession {
    session_id: u32,
    seq: AtomicU32,
    last_activity: RwLock<Instant>,
    response_dedup: RwLock<ResponseDedupWindow>,
}

/// TCP session tracks a session for TCP transport.
struct TcpSession {
    writer: Mutex<WriteHalf<TcpStream>>,
    last_activity: RwLock<Instant>,
    cancel_token: CancellationToken,
}

/// Client handles UDP packets from sources and forwards them to servers.
pub struct Client {
    cfg: ClientConfig,
    cancel_token: CancellationToken,
    listener: RwLock<Option<Arc<UdpSocket>>>,
    listen_addr: RwLock<Option<SocketAddr>>,

    /// Session ID counter for UDP transport.
    session_id_counter: AtomicU32,

    /// For UDP transport: map sourceAddr -> UdpSession.
    udp_sessions: RwLock<HashMap<SocketAddr, Arc<UdpSession>>>,

    /// Reverse map for UDP: sessionID -> sourceAddr.
    udp_session_addrs: RwLock<HashMap<u32, SocketAddr>>,

    /// For TCP transport: map (sourceAddr, serverAddr) -> TcpSession.
    tcp_sessions: RwLock<HashMap<String, Arc<TcpSession>>>,

    /// UDP senders (one per UDP server endpoint, shared by all sessions).
    udp_senders: RwLock<Vec<Arc<UdpSocket>>>,
}

impl Client {
    /// Creates a new Client instance.
    pub fn new(cfg: ClientConfig) -> Self {
        Self {
            cfg,
            cancel_token: CancellationToken::new(),
            listener: RwLock::new(None),
            listen_addr: RwLock::new(None),
            // Use random initial value to avoid conflicts after client restart
            session_id_counter: AtomicU32::new(rand::random()),
            udp_sessions: RwLock::new(HashMap::new()),
            udp_session_addrs: RwLock::new(HashMap::new()),
            tcp_sessions: RwLock::new(HashMap::new()),
            udp_senders: RwLock::new(Vec::new()),
        }
    }

    /// Starts the client.
    pub async fn start(self: Arc<Self>) -> io::Result<SocketAddr> {
        // Create UDP senders for each UDP server endpoint
        for endpoint in &self.cfg.servers {
            if endpoint.protocol == Protocol::Udp {
                let socket = UdpSocket::bind("0.0.0.0:0").await?;
                socket.connect(&endpoint.address).await?;
                let socket = Arc::new(socket);

                info!("connected to UDP server {}", endpoint.address);

                // Start response receiver for this UDP sender
                let client = Arc::clone(&self);
                let socket_clone = Arc::clone(&socket);
                tokio::spawn(async move {
                    client.handle_udp_server_response(socket_clone).await;
                });

                self.udp_senders.write().await.push(socket);
            }
        }

        // Start UDP listener for sources
        let socket = UdpSocket::bind(&self.cfg.listen_addr).await?;
        let addr = socket.local_addr()?;
        let socket = Arc::new(socket);

        *self.listener.write().await = Some(Arc::clone(&socket));
        *self.listen_addr.write().await = Some(addr);

        info!("listening on UDP {}", addr);

        // Start listener handler
        let client = Arc::clone(&self);
        tokio::spawn(async move {
            client.handle_listener(socket).await;
        });

        // Start session cleanup loop
        let client = Arc::clone(&self);
        tokio::spawn(async move {
            client.session_cleanup_loop().await;
        });

        Ok(addr)
    }

    /// Stops the client gracefully.
    pub fn stop(&self) {
        self.cancel_token.cancel();
        info!("client stopped");
    }

    /// Returns the listen address.
    pub async fn listen_addr(&self) -> Option<SocketAddr> {
        *self.listen_addr.read().await
    }

    async fn handle_listener(self: Arc<Self>, socket: Arc<UdpSocket>) {
        let mut buf = vec![0u8; 65535];

        loop {
            tokio::select! {
                _ = self.cancel_token.cancelled() => {
                    return;
                }
                result = socket.recv_from(&mut buf) => {
                    match result {
                        Ok((n, source_addr)) => {
                            self.handle_packet(source_addr, &buf[..n]).await;
                        }
                        Err(e) => {
                            if self.cancel_token.is_cancelled() {
                                return;
                            }
                            error!("UDP read error: {}", e);
                        }
                    }
                }
            }
        }
    }

    async fn handle_packet(self: &Arc<Self>, source_addr: SocketAddr, payload: &[u8]) {
        // Get or create session for this source (shared across all transports)
        let session = self.get_or_create_udp_session(source_addr).await;

        // Get next sequence number ONCE for all paths (for deduplication consistency)
        let seq = session.seq.fetch_add(1, Ordering::SeqCst);

        // Update last activity
        let now = Instant::now();
        *session.last_activity.write().await = now;

        // Handle UDP transport endpoints
        self.handle_udp_transport(&session, seq, payload).await;

        // Handle TCP transport endpoints
        self.handle_tcp_transport(source_addr, &session, seq, payload)
            .await;
    }

    async fn handle_udp_transport(self: &Arc<Self>, session: &UdpSession, seq: u32, payload: &[u8]) {
        // Encode with session ID for UDP transport
        let packet = protocol::encode_udp(session.session_id, seq, payload);

        // Send to all UDP server endpoints
        let senders = self.udp_senders.read().await;
        for (i, sender) in senders.iter().enumerate() {
            if let Err(e) = sender.send(&packet).await {
                error!("UDP send error endpoint={} error={}", i, e);
            }
        }

        debug!(
            "forwarded packet via UDP session_id={} seq={} size={}",
            session.session_id,
            seq,
            payload.len()
        );
    }

    async fn get_or_create_udp_session(
        self: &Arc<Self>,
        source_addr: SocketAddr,
    ) -> Arc<UdpSession> {
        // Try read lock first
        {
            let sessions = self.udp_sessions.read().await;
            if let Some(session) = sessions.get(&source_addr) {
                return Arc::clone(session);
            }
        }

        // Create new session
        let mut sessions = self.udp_sessions.write().await;

        // Double-check after acquiring write lock
        if let Some(session) = sessions.get(&source_addr) {
            return Arc::clone(session);
        }

        let mut session_id = self.session_id_counter.fetch_add(1, Ordering::SeqCst).wrapping_add(1);
        // Ensure session_id is never 0 (0 is often treated as invalid/uninitialized).
        // Must increment counter again to avoid duplicate IDs after wrap.
        if session_id == 0 {
            session_id = self.session_id_counter.fetch_add(1, Ordering::SeqCst).wrapping_add(1);
        }
        // Use random initial seq to avoid conflicts with server's old dedup window after restart.
        // Limit to first half of u32 range to avoid quick wrap-around which would cause
        // the server's dedup window to reject new packets (seq < min_seq treated as duplicate).
        let initial_seq: u32 = rand::random::<u32>() % (u32::MAX / 2);
        let session = Arc::new(UdpSession {
            session_id,
            seq: AtomicU32::new(initial_seq),
            last_activity: RwLock::new(Instant::now()),
            response_dedup: RwLock::new(ResponseDedupWindow::new(DEFAULT_RESPONSE_DEDUP_SIZE)),
        });

        sessions.insert(source_addr, Arc::clone(&session));

        // Store reverse mapping
        self.udp_session_addrs
            .write()
            .await
            .insert(session_id, source_addr);

        info!(
            "created UDP session session_id={} source={}",
            session_id, source_addr
        );

        session
    }

    async fn handle_udp_server_response(self: Arc<Self>, server_conn: Arc<UdpSocket>) {
        let mut buf = vec![0u8; 65535];

        loop {
            tokio::select! {
                _ = self.cancel_token.cancelled() => {
                    return;
                }
                result = server_conn.recv(&mut buf) => {
                    match result {
                        Ok(n) => {
                            self.process_udp_response(&buf[..n]).await;
                        }
                        Err(e) => {
                            if self.cancel_token.is_cancelled() {
                                return;
                            }
                            error!("UDP server read error: {}", e);
                        }
                    }
                }
            }
        }
    }

    async fn process_udp_response(&self, data: &[u8]) {
        let (session_id, seq, payload) = match protocol::decode_udp(data) {
            Ok(decoded) => decoded,
            Err(e) => {
                error!("UDP response decode error: {}", e);
                return;
            }
        };

        // Lookup source address for this session
        let source_addr = {
            let addrs = self.udp_session_addrs.read().await;
            addrs.get(&session_id).copied()
        };

        let source_addr = match source_addr {
            Some(addr) => addr,
            None => {
                warn!("no source address for session {}", session_id);
                return;
            }
        };

        // Find session and check for duplicate response
        let session = {
            let sessions = self.udp_sessions.read().await;
            sessions.get(&source_addr).cloned()
        };

        let session = match session {
            Some(s) => s,
            None => {
                warn!("session not found for response session_id={}", session_id);
                return;
            }
        };

        // Check for duplicate response
        if session.response_dedup.write().await.is_duplicate(seq) {
            debug!(
                "duplicate UDP response discarded session_id={} seq={}",
                session_id, seq
            );
            return;
        }

        // Update session activity
        *session.last_activity.write().await = Instant::now();

        // Forward response to source
        let listener = self.listener.read().await;
        if let Some(ref listener) = *listener {
            if let Err(e) = listener.send_to(payload, source_addr).await {
                error!(
                    "failed to forward response to source session_id={} error={}",
                    session_id, e
                );
            } else {
                debug!(
                    "forwarded response to source session_id={} seq={} size={}",
                    session_id,
                    seq,
                    payload.len()
                );
            }
        }
    }

    async fn handle_tcp_transport(
        self: &Arc<Self>,
        source_addr: SocketAddr,
        udp_session: &UdpSession,
        seq: u32,
        payload: &[u8],
    ) {
        let session_id = udp_session.session_id;

        // For each TCP server endpoint, get or create a dedicated TCP session
        for endpoint in &self.cfg.servers {
            if endpoint.protocol != Protocol::Tcp {
                continue;
            }

            let session_key = format!("{}->{}", source_addr, endpoint.address);

            let (session, created) = self
                .get_or_create_tcp_session(source_addr, &endpoint.address, &session_key)
                .await;

            let session = match session {
                Some(s) => s,
                None => continue,
            };

            if created {
                info!(
                    "created TCP session source={} server={}",
                    source_addr, endpoint.address
                );
            }

            // Update last activity
            *session.last_activity.write().await = Instant::now();

            // Encode with TCP format (use same seq as UDP for deduplication)
            let packet = protocol::encode_tcp(session_id, seq, payload);

            // Send with length prefix
            if let Err(e) = self.send_tcp_frame(&session, &packet).await {
                error!("TCP send error source={} error={}", source_addr, e);
                continue;
            }

            debug!(
                "forwarded packet via TCP session_id={} seq={} size={}",
                session_id,
                seq,
                payload.len()
            );
        }
    }

    async fn get_or_create_tcp_session(
        self: &Arc<Self>,
        source_addr: SocketAddr,
        server_addr: &str,
        session_key: &str,
    ) -> (Option<Arc<TcpSession>>, bool) {
        // Try read lock first
        {
            let sessions = self.tcp_sessions.read().await;
            if let Some(session) = sessions.get(session_key) {
                return (Some(Arc::clone(session)), false);
            }
        }

        // Create new session
        let mut sessions = self.tcp_sessions.write().await;

        // Double-check after acquiring write lock
        if let Some(session) = sessions.get(session_key) {
            return (Some(Arc::clone(session)), false);
        }

        // Create new TCP connection
        let stream = match TcpStream::connect(server_addr).await {
            Ok(s) => s,
            Err(e) => {
                error!("failed to connect to TCP server {}: {}", server_addr, e);
                return (None, false);
            }
        };

        // Split the stream for separate read/write
        let (reader, writer) = tokio::io::split(stream);

        let cancel_token = CancellationToken::new();
        let session = Arc::new(TcpSession {
            writer: Mutex::new(writer),
            last_activity: RwLock::new(Instant::now()),
            cancel_token: cancel_token.clone(),
        });

        sessions.insert(session_key.to_string(), Arc::clone(&session));

        // Start response receiver for this TCP connection
        let client = Arc::clone(self);
        let session_key_clone = session_key.to_string();
        let client_cancel = self.cancel_token.clone();
        tokio::spawn(async move {
            client
                .handle_tcp_server_response(
                    source_addr,
                    session_key_clone,
                    reader,
                    cancel_token,
                    client_cancel,
                )
                .await;
        });

        (Some(session), true)
    }

    async fn send_tcp_frame(&self, session: &TcpSession, data: &[u8]) -> io::Result<()> {
        if data.len() > MAX_TCP_FRAME_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "frame too large",
            ));
        }

        let mut frame = Vec::with_capacity(2 + data.len());
        frame.extend_from_slice(&(data.len() as u16).to_be_bytes());
        frame.extend_from_slice(data);

        let mut writer = session.writer.lock().await;
        writer.write_all(&frame).await
    }

    async fn handle_tcp_server_response(
        self: Arc<Self>,
        source_addr: SocketAddr,
        session_key: String,
        mut reader: tokio::io::ReadHalf<TcpStream>,
        session_cancel: CancellationToken,
        client_cancel: CancellationToken,
    ) {
        let mut len_buf = [0u8; 2];

        loop {
            tokio::select! {
                _ = session_cancel.cancelled() => {
                    return;
                }
                _ = client_cancel.cancelled() => {
                    return;
                }
                result = reader.read_exact(&mut len_buf) => {
                    match result {
                        Ok(_) => {}
                        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => {
                            self.cleanup_tcp_session(&session_key).await;
                            return;
                        }
                        Err(e) => {
                            if !client_cancel.is_cancelled() && !session_cancel.is_cancelled() {
                                error!("TCP response read length error: {}", e);
                            }
                            self.cleanup_tcp_session(&session_key).await;
                            return;
                        }
                    }
                }
            }

            let length = u16::from_be_bytes(len_buf) as usize;
            if length == 0 || length > MAX_TCP_FRAME_SIZE {
                error!("invalid TCP response length: {}", length);
                self.cleanup_tcp_session(&session_key).await;
                return;
            }

            let mut data = vec![0u8; length];
            if let Err(e) = reader.read_exact(&mut data).await {
                if !client_cancel.is_cancelled() && !session_cancel.is_cancelled() {
                    error!("TCP response read data error: {}", e);
                }
                self.cleanup_tcp_session(&session_key).await;
                return;
            }

            self.process_tcp_response(source_addr, &data).await;
        }
    }

    async fn process_tcp_response(&self, source_addr: SocketAddr, data: &[u8]) {
        let (session_id, seq, payload) = match protocol::decode_tcp(data) {
            Ok(decoded) => decoded,
            Err(e) => {
                error!("TCP response decode error: {}", e);
                return;
            }
        };

        // Find session and check for duplicate response (uses shared dedup window)
        let udp_session = {
            let sessions = self.udp_sessions.read().await;
            sessions.get(&source_addr).cloned()
        };

        let udp_session = match udp_session {
            Some(s) => s,
            None => {
                warn!(
                    "session not found for TCP response session_id={}",
                    session_id
                );
                return;
            }
        };

        // Check for duplicate response (shared with UDP path)
        if udp_session.response_dedup.write().await.is_duplicate(seq) {
            debug!(
                "duplicate TCP response discarded session_id={} seq={}",
                session_id, seq
            );
            return;
        }

        // Update session activity
        *udp_session.last_activity.write().await = Instant::now();

        // Forward response to source
        let listener = self.listener.read().await;
        if let Some(ref listener) = *listener {
            if let Err(e) = listener.send_to(payload, source_addr).await {
                error!(
                    "failed to forward TCP response to source session_id={} error={}",
                    session_id, e
                );
            } else {
                debug!(
                    "forwarded TCP response to source session_id={} seq={} size={}",
                    session_id,
                    seq,
                    payload.len()
                );
            }
        }
    }

    async fn cleanup_tcp_session(&self, session_key: &str) {
        let mut sessions = self.tcp_sessions.write().await;
        if let Some(session) = sessions.remove(session_key) {
            session.cancel_token.cancel();
            info!("cleaned up TCP session {}", session_key);
        }
    }

    async fn session_cleanup_loop(self: Arc<Self>) {
        let interval = self.cfg.session_timeout.min(Duration::from_secs(10));
        let interval = interval.max(Duration::from_millis(100));

        let mut ticker = time::interval(interval);

        loop {
            tokio::select! {
                _ = self.cancel_token.cancelled() => {
                    return;
                }
                _ = ticker.tick() => {
                    self.cleanup_expired_sessions().await;
                }
            }
        }
    }

    async fn cleanup_expired_sessions(&self) {
        let now = Instant::now();
        let timeout = self.cfg.session_timeout;

        // Cleanup UDP sessions
        {
            let mut sessions = self.udp_sessions.write().await;
            let mut expired = Vec::new();

            for (source_addr, session) in sessions.iter() {
                let last_activity = *session.last_activity.read().await;
                if now.duration_since(last_activity) > timeout {
                    expired.push((*source_addr, session.session_id));
                }
            }

            for (source_addr, session_id) in expired {
                info!(
                    "cleaning up expired UDP session session_id={} source={}",
                    session_id, source_addr
                );
                sessions.remove(&source_addr);
                self.udp_session_addrs.write().await.remove(&session_id);
            }
        }

        // Cleanup TCP sessions
        {
            let mut sessions = self.tcp_sessions.write().await;
            let mut expired = Vec::new();

            for (session_key, session) in sessions.iter() {
                let last_activity = *session.last_activity.read().await;
                if now.duration_since(last_activity) > timeout {
                    expired.push(session_key.clone());
                }
            }

            for session_key in expired {
                info!("cleaning up expired TCP session {}", session_key);
                if let Some(session) = sessions.remove(&session_key) {
                    session.cancel_token.cancel();
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_response_dedup_window_first_packet() {
        let mut window = ResponseDedupWindow::new(100);
        assert!(!window.is_duplicate(0));
        assert!(window.is_duplicate(0));
    }

    #[test]
    fn test_response_dedup_window_sequential() {
        let mut window = ResponseDedupWindow::new(100);
        for i in 0..50 {
            assert!(!window.is_duplicate(i));
        }
        for i in 0..50 {
            assert!(window.is_duplicate(i));
        }
    }

    #[test]
    fn test_response_dedup_window_sliding() {
        let mut window = ResponseDedupWindow::new(10);

        // Fill window
        for i in 0..10 {
            assert!(!window.is_duplicate(i));
        }

        // Old packets should be treated as duplicates when window slides
        assert!(!window.is_duplicate(15)); // This slides the window
        assert!(window.is_duplicate(0)); // Now too old
        assert!(window.is_duplicate(4)); // Also too old
    }

    #[test]
    fn test_response_dedup_window_before_start() {
        let mut window = ResponseDedupWindow::new(100);
        assert!(!window.is_duplicate(10)); // First packet at 10
        assert!(window.is_duplicate(5)); // Before the window start
    }
}
