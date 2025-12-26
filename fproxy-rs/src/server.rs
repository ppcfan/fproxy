//! Server mode implementation for fproxy.
//!
//! The server listens on one or more UDP/TCP endpoints, deduplicates received packets,
//! creates dedicated target UDP sockets per session, forwards unique packets to the target,
//! and forwards responses back to the originating client.

use crate::config::{Protocol, ServerConfig};
use crate::protocol;
use std::collections::HashMap;
use std::io;
use std::net::SocketAddr;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::io::{AsyncReadExt, AsyncWriteExt, WriteHalf};
use tokio::net::{TcpListener, TcpStream, UdpSocket};
use tokio::sync::{Mutex, RwLock};
use tokio::time;
use tokio_util::sync::CancellationToken;
use tracing::{debug, error, info, warn};

/// Maximum TCP frame size.
const MAX_TCP_FRAME_SIZE: usize = 65535;

/// Deduplication window for tracking seen sequence numbers.
pub struct DedupWindow {
    seen: HashMap<u32, ()>,
    min_seq: u32,
    max_seq: u32,
    size: usize,
    started: bool,
}

impl DedupWindow {
    /// Creates a new deduplication window with the given size.
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

        // Check if already seen
        if self.seen.contains_key(&seq) {
            return true;
        }

        // Check if sequence is too old (before window)
        if seq < self.min_seq {
            return true;
        }

        // Mark as seen
        self.seen.insert(seq, ());

        // Slide window if needed
        if seq > self.max_seq {
            self.max_seq = seq;
            // Clean up old entries if window is too large
            if self.seen.len() > self.size {
                let new_min = self.max_seq.saturating_sub(self.size as u32 - 1);
                self.seen.retain(|&s, _| s >= new_min);
                self.min_seq = new_min;
            }
        }

        false
    }
}

/// Client path for sending responses.
pub enum ClientPath {
    Udp {
        addr: SocketAddr,
        socket: Arc<UdpSocket>,
    },
    Tcp {
        conn_id: usize,
        writer: Arc<Mutex<WriteHalf<TcpStream>>>,
    },
}

/// Target session with dedicated target connection.
struct TargetSession {
    target_conn: Arc<UdpSocket>,
    dedup_window: RwLock<DedupWindow>,
    response_seq: AtomicU32,
    last_activity: RwLock<Instant>,
    cancel_token: CancellationToken,
}

/// Server handles incoming UDP/TCP packets and forwards them to the target.
pub struct Server {
    cfg: ServerConfig,
    cancel_token: CancellationToken,

    /// Sessions by session_id.
    sessions: RwLock<HashMap<u64, Arc<TargetSession>>>,

    /// Client paths per session_id.
    client_paths: RwLock<HashMap<u64, Vec<Arc<ClientPath>>>>,

    /// Track which session_id each TCP connection belongs to.
    tcp_conn_to_session: RwLock<HashMap<usize, u64>>,
}

impl Server {
    /// Creates a new Server instance.
    pub fn new(cfg: ServerConfig) -> Self {
        Self {
            cfg,
            cancel_token: CancellationToken::new(),
            sessions: RwLock::new(HashMap::new()),
            client_paths: RwLock::new(HashMap::new()),
            tcp_conn_to_session: RwLock::new(HashMap::new()),
        }
    }

    /// Starts the server.
    pub async fn start(self: Arc<Self>) -> io::Result<Vec<SocketAddr>> {
        let mut listen_addrs = Vec::new();

        for endpoint in &self.cfg.listen_addrs {
            match endpoint.protocol {
                Protocol::Udp => {
                    let socket = UdpSocket::bind(&endpoint.address).await?;
                    let addr = socket.local_addr()?;
                    listen_addrs.push(addr);
                    info!("listening on UDP {}", addr);

                    let server = Arc::clone(&self);
                    let socket = Arc::new(socket);
                    tokio::spawn(async move {
                        server.handle_udp_listener(socket).await;
                    });
                }
                Protocol::Tcp => {
                    let listener = TcpListener::bind(&endpoint.address).await?;
                    let addr = listener.local_addr()?;
                    listen_addrs.push(addr);
                    info!("listening on TCP {}", addr);

                    let server = Arc::clone(&self);
                    tokio::spawn(async move {
                        server.handle_tcp_listener(listener).await;
                    });
                }
            }
        }

        // Start session cleanup loop
        let server = Arc::clone(&self);
        tokio::spawn(async move {
            server.session_cleanup_loop().await;
        });

        Ok(listen_addrs)
    }

    /// Stops the server gracefully.
    pub fn stop(&self) {
        self.cancel_token.cancel();
        info!("server stopped");
    }

    /// Returns the number of active sessions.
    pub async fn session_count(&self) -> usize {
        self.sessions.read().await.len()
    }

    async fn handle_udp_listener(self: Arc<Self>, socket: Arc<UdpSocket>) {
        let mut buf = vec![0u8; 65535];

        loop {
            tokio::select! {
                _ = self.cancel_token.cancelled() => {
                    return;
                }
                result = socket.recv_from(&mut buf) => {
                    match result {
                        Ok((n, client_addr)) => {
                            self.handle_udp_packet(Arc::clone(&socket), client_addr, &buf[..n]).await;
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

    async fn handle_udp_packet(
        self: &Arc<Self>,
        listener: Arc<UdpSocket>,
        client_addr: SocketAddr,
        data: &[u8],
    ) {
        let (session_id, seq, payload) = match protocol::decode_udp(data) {
            Ok(decoded) => decoded,
            Err(e) => {
                error!("UDP packet decode error: {}", e);
                return;
            }
        };

        // Get or create session
        let (session, created) = match self.get_or_create_session(session_id).await {
            Some(result) => result,
            None => {
                warn!(
                    "failed to create session for UDP packet session_id={}",
                    session_id
                );
                return;
            }
        };
        if created {
            debug!("created new session {}", session_id);
        }

        // Update last activity
        *session.last_activity.write().await = Instant::now();

        // Check for duplicate
        if session.dedup_window.write().await.is_duplicate(seq) {
            debug!(
                "duplicate packet discarded session_id={} seq={}",
                session_id, seq
            );
            return;
        }

        // Add/update UDP path
        self.add_udp_path(session_id, client_addr, Arc::clone(&listener))
            .await;

        // Forward to target
        if let Err(e) = session.target_conn.send(payload).await {
            error!(
                "failed to forward to target session_id={} error={}",
                session_id, e
            );
            return;
        }

        debug!(
            "forwarded UDP packet session_id={} seq={} size={}",
            session_id,
            seq,
            payload.len()
        );
    }

    async fn get_or_create_session(
        self: &Arc<Self>,
        session_id: u64,
    ) -> Option<(Arc<TargetSession>, bool)> {
        // Try read lock first
        {
            let sessions = self.sessions.read().await;
            if let Some(session) = sessions.get(&session_id) {
                return Some((Arc::clone(session), false));
            }
        }

        // Create new session
        let mut sessions = self.sessions.write().await;

        // Double-check after acquiring write lock
        if let Some(session) = sessions.get(&session_id) {
            return Some((Arc::clone(session), false));
        }

        // Create target connection
        let target_conn = match UdpSocket::bind("0.0.0.0:0").await {
            Ok(socket) => socket,
            Err(e) => {
                error!("failed to bind target socket: {}", e);
                return None;
            }
        };

        if let Err(e) = target_conn.connect(&self.cfg.target_addr).await {
            error!("failed to connect to target: {}", e);
            return None;
        }

        let target_conn = Arc::new(target_conn);

        // Create per-session cancel token (child of server token)
        let session_cancel = self.cancel_token.child_token();

        let session = Arc::new(TargetSession {
            target_conn: Arc::clone(&target_conn),
            dedup_window: RwLock::new(DedupWindow::new(self.cfg.dedup_window)),
            response_seq: AtomicU32::new(0),
            last_activity: RwLock::new(Instant::now()),
            cancel_token: session_cancel.clone(),
        });

        sessions.insert(session_id, Arc::clone(&session));

        info!(
            "created target session session_id={} target={}",
            session_id, self.cfg.target_addr
        );

        // Start response handler with per-session cancel token
        let server = Arc::clone(self);
        let target_conn_clone = target_conn;
        tokio::spawn(async move {
            server
                .handle_target_response(session_cancel, session_id, target_conn_clone)
                .await;
        });

        Some((session, true))
    }

    async fn add_udp_path(&self, session_id: u64, client_addr: SocketAddr, socket: Arc<UdpSocket>) {
        let mut paths = self.client_paths.write().await;
        let session_paths = paths.entry(session_id).or_default();

        // Check if this UDP path already exists (same socket) and update address
        for path in session_paths.iter_mut() {
            if let ClientPath::Udp { socket: s, addr } = path.as_ref() {
                if Arc::ptr_eq(s, &socket) {
                    // Update address if changed (for NAT/mobility)
                    if *addr != client_addr {
                        debug!(
                            "updating UDP path addr from {} to {} for session {}",
                            addr, client_addr, session_id
                        );
                        *path = Arc::new(ClientPath::Udp {
                            addr: client_addr,
                            socket: Arc::clone(s),
                        });
                    }
                    return;
                }
            }
        }

        // Add new UDP path
        session_paths.push(Arc::new(ClientPath::Udp {
            addr: client_addr,
            socket,
        }));
    }

    async fn handle_target_response(
        self: Arc<Self>,
        cancel_token: CancellationToken,
        session_id: u64,
        target_conn: Arc<UdpSocket>,
    ) {
        let mut buf = vec![0u8; 65535];

        loop {
            tokio::select! {
                _ = cancel_token.cancelled() => {
                    return;
                }
                result = target_conn.recv(&mut buf) => {
                    match result {
                        Ok(n) => {
                            self.send_response_to_all_paths(session_id, &buf[..n]).await;
                        }
                        Err(e) => {
                            if cancel_token.is_cancelled() {
                                return;
                            }
                            error!("target read error session_id={} error={}", session_id, e);
                            return;
                        }
                    }
                }
            }
        }
    }

    async fn send_response_to_all_paths(&self, session_id: u64, payload: &[u8]) {
        // Update last activity
        {
            let sessions = self.sessions.read().await;
            if let Some(session) = sessions.get(&session_id) {
                *session.last_activity.write().await = Instant::now();
            }
        }

        // Get response sequence number
        let seq = {
            let sessions = self.sessions.read().await;
            if let Some(session) = sessions.get(&session_id) {
                session.response_seq.fetch_add(1, Ordering::SeqCst)
            } else {
                return;
            }
        };

        // Get all client paths
        let paths: Vec<Arc<ClientPath>> = {
            let paths_guard = self.client_paths.read().await;
            match paths_guard.get(&session_id) {
                Some(p) => p.clone(),
                None => {
                    warn!("no client paths for session {}", session_id);
                    return;
                }
            }
        };

        if paths.is_empty() {
            warn!("no client paths for session {}", session_id);
            return;
        }

        // Send to all paths
        for path in &paths {
            match path.as_ref() {
                ClientPath::Udp { addr, socket } => {
                    let response = protocol::encode_udp(session_id, seq, payload);
                    if let Err(e) = socket.send_to(&response, addr).await {
                        error!(
                            "failed to send UDP response session_id={} error={}",
                            session_id, e
                        );
                    }
                }
                ClientPath::Tcp { writer, .. } => {
                    let response = protocol::encode_tcp(session_id, seq, payload);
                    let mut frame = Vec::with_capacity(2 + response.len());
                    frame.extend_from_slice(&(response.len() as u16).to_be_bytes());
                    frame.extend_from_slice(&response);

                    let mut writer_guard = writer.lock().await;
                    if let Err(e) = writer_guard.write_all(&frame).await {
                        error!(
                            "failed to send TCP response session_id={} error={}",
                            session_id, e
                        );
                    }
                }
            }
        }

        debug!(
            "sent response to all paths session_id={} seq={} paths={} size={}",
            session_id,
            seq,
            paths.len(),
            payload.len()
        );
    }

    async fn handle_tcp_listener(self: Arc<Self>, listener: TcpListener) {
        loop {
            tokio::select! {
                _ = self.cancel_token.cancelled() => {
                    return;
                }
                result = listener.accept() => {
                    match result {
                        Ok((stream, client_addr)) => {
                            info!("TCP connection established client={}", client_addr);
                            let server = Arc::clone(&self);
                            tokio::spawn(async move {
                                server.handle_tcp_conn(stream).await;
                            });
                        }
                        Err(e) => {
                            if self.cancel_token.is_cancelled() {
                                return;
                            }
                            error!("TCP accept error: {}", e);
                        }
                    }
                }
            }
        }
    }

    async fn handle_tcp_conn(self: Arc<Self>, stream: TcpStream) {
        let peer_addr = stream.peer_addr().ok();

        // Split the stream for reading and writing
        let (mut reader, writer) = tokio::io::split(stream);
        let writer = Arc::new(Mutex::new(writer));
        let conn_id = Arc::as_ptr(&writer) as usize;

        let mut len_buf = [0u8; 2];

        loop {
            tokio::select! {
                _ = self.cancel_token.cancelled() => {
                    break;
                }
                result = reader.read_exact(&mut len_buf) => {
                    match result {
                        Ok(_) => {}
                        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => {
                            break;
                        }
                        Err(e) => {
                            if !self.cancel_token.is_cancelled() {
                                error!("TCP read length error: {}", e);
                            }
                            break;
                        }
                    }
                }
            }

            let length = u16::from_be_bytes(len_buf) as usize;
            if length == 0 || length > MAX_TCP_FRAME_SIZE {
                error!("invalid TCP packet length: {}", length);
                break;
            }

            let mut data = vec![0u8; length];
            if let Err(e) = reader.read_exact(&mut data).await {
                if !self.cancel_token.is_cancelled() {
                    error!("TCP read data error: {}", e);
                }
                break;
            }

            self.handle_tcp_packet(conn_id, Arc::clone(&writer), &data)
                .await;
        }

        // Cleanup
        self.cleanup_tcp_conn(conn_id).await;
        if let Some(addr) = peer_addr {
            info!("TCP connection closed client={}", addr);
        }
    }

    async fn handle_tcp_packet(
        self: &Arc<Self>,
        conn_id: usize,
        writer: Arc<Mutex<WriteHalf<TcpStream>>>,
        data: &[u8],
    ) {
        let (session_id, seq, payload) = match protocol::decode_tcp(data) {
            Ok(decoded) => decoded,
            Err(e) => {
                error!("TCP packet decode error: {}", e);
                return;
            }
        };

        // Get or create session
        let (session, created) = match self.get_or_create_session(session_id).await {
            Some(result) => result,
            None => {
                warn!(
                    "failed to create session for TCP packet session_id={}",
                    session_id
                );
                return;
            }
        };

        if created {
            debug!("created new session via TCP {}", session_id);
        }

        // Update last activity
        *session.last_activity.write().await = Instant::now();

        // Check for duplicate
        if session.dedup_window.write().await.is_duplicate(seq) {
            debug!(
                "duplicate TCP packet discarded session_id={} seq={}",
                session_id, seq
            );
            return;
        }

        // Add TCP path
        self.add_tcp_path(session_id, conn_id, writer).await;

        // Forward to target
        if let Err(e) = session.target_conn.send(payload).await {
            error!(
                "failed to forward TCP packet to target session_id={} error={}",
                session_id, e
            );
            return;
        }

        debug!(
            "forwarded TCP packet session_id={} seq={} size={}",
            session_id,
            seq,
            payload.len()
        );
    }

    async fn add_tcp_path(
        &self,
        session_id: u64,
        conn_id: usize,
        writer: Arc<Mutex<WriteHalf<TcpStream>>>,
    ) {
        // Track session for this connection
        self.tcp_conn_to_session
            .write()
            .await
            .insert(conn_id, session_id);

        let mut paths = self.client_paths.write().await;
        let session_paths = paths.entry(session_id).or_default();

        // Check if this TCP path already exists (same conn_id)
        for path in session_paths.iter() {
            if let ClientPath::Tcp {
                conn_id: cid,
                writer: w,
            } = path.as_ref()
            {
                if *cid == conn_id || Arc::ptr_eq(w, &writer) {
                    return;
                }
            }
        }

        // Add new TCP path with conn_id
        session_paths.push(Arc::new(ClientPath::Tcp { conn_id, writer }));
    }

    async fn cleanup_tcp_conn(&self, conn_id: usize) {
        let session_id = {
            let mut conn_map = self.tcp_conn_to_session.write().await;
            conn_map.remove(&conn_id)
        };

        if let Some(session_id) = session_id {
            let mut paths = self.client_paths.write().await;
            if let Some(session_paths) = paths.get_mut(&session_id) {
                // Only remove the specific TCP path with this conn_id
                session_paths.retain(|p| {
                    !matches!(p.as_ref(), ClientPath::Tcp { conn_id: cid, .. } if *cid == conn_id)
                });
            }
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

        let mut sessions = self.sessions.write().await;
        let mut expired = Vec::new();

        for (&session_id, session) in sessions.iter() {
            let last_activity = *session.last_activity.read().await;
            if now.duration_since(last_activity) > timeout {
                expired.push(session_id);
            }
        }

        for session_id in expired {
            info!("cleaning up expired session {}", session_id);
            // Cancel the session's response handler task
            if let Some(session) = sessions.remove(&session_id) {
                session.cancel_token.cancel();
            }

            // Remove client paths
            self.client_paths.write().await.remove(&session_id);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dedup_window_first_packet() {
        let mut window = DedupWindow::new(100);
        assert!(!window.is_duplicate(0));
        assert!(window.is_duplicate(0));
    }

    #[test]
    fn test_dedup_window_sequential() {
        let mut window = DedupWindow::new(100);
        for i in 0..50 {
            assert!(!window.is_duplicate(i));
        }
        for i in 0..50 {
            assert!(window.is_duplicate(i));
        }
    }

    #[test]
    fn test_dedup_window_sliding() {
        let mut window = DedupWindow::new(10);

        // Fill window
        for i in 0..10 {
            assert!(!window.is_duplicate(i));
        }

        // Old packets should be treated as duplicates when window slides
        assert!(!window.is_duplicate(15)); // This slides the window
        assert!(window.is_duplicate(0)); // Now too old
        assert!(window.is_duplicate(4)); // Also too old
        assert!(!window.is_duplicate(10)); // Still in window
    }

    #[test]
    fn test_dedup_window_out_of_order() {
        let mut window = DedupWindow::new(100);
        assert!(!window.is_duplicate(5));
        // seq=3 is before window start (min_seq=5), treated as old/duplicate
        assert!(window.is_duplicate(3));
        assert!(!window.is_duplicate(7));
        assert!(window.is_duplicate(5));
        assert!(window.is_duplicate(7));
    }

    #[test]
    fn test_dedup_window_out_of_order_within_window() {
        let mut window = DedupWindow::new(100);
        assert!(!window.is_duplicate(0)); // Start at 0
        assert!(!window.is_duplicate(5)); // Skip ahead
        assert!(!window.is_duplicate(3)); // Out of order but within window
        assert!(!window.is_duplicate(7));
        assert!(window.is_duplicate(3)); // Now it's a duplicate
        assert!(window.is_duplicate(5));
    }

    #[test]
    fn test_dedup_window_before_start() {
        let mut window = DedupWindow::new(100);
        assert!(!window.is_duplicate(10)); // First packet at 10
        assert!(window.is_duplicate(5)); // Before the window start
    }
}
