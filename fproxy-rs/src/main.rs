//! fproxy - A Layer 4 bidirectional UDP relay with multi-path transmission and deduplication.

use anyhow::Result;
use clap::Parser;
use fproxy::client::Client;
use fproxy::config::{load, CliArgs, Mode};
use fproxy::server::Server;
use std::sync::Arc;
use tokio::signal;
use tracing::{info, Level};
use tracing_subscriber::FmtSubscriber;

/// Converts Go-style flags (-flag) to clap-style (--flag).
fn convert_go_style_args() -> Vec<String> {
    let known_flags = [
        "config",
        "mode",
        "verbose",
        "listen",
        "servers",
        "listen-addrs",
        "target",
        "dedup-window",
        "session-timeout",
        "help",
        "version",
    ];

    std::env::args()
        .map(|arg| {
            // Check if it's a single-dash flag that matches a known flag
            if let Some(rest) = arg.strip_prefix('-') {
                if !rest.starts_with('-') {
                    // Extract flag name (handle -flag=value format)
                    let flag_name = rest.split('=').next().unwrap_or(rest);
                    if known_flags.contains(&flag_name) {
                        return format!("-{}", arg);
                    }
                }
            }
            arg
        })
        .collect()
}

#[tokio::main]
async fn main() -> Result<()> {
    // Parse CLI arguments (convert Go-style -flag to --flag)
    let args = CliArgs::parse_from(convert_go_style_args());

    // Load configuration
    let config = load(args)?;

    // Setup logging
    let log_level = if config.verbose {
        Level::DEBUG
    } else {
        Level::INFO
    };

    let subscriber = FmtSubscriber::builder()
        .with_max_level(log_level)
        .with_target(false)
        .with_thread_ids(false)
        .with_file(false)
        .with_line_number(false)
        .finish();

    tracing::subscriber::set_global_default(subscriber)?;

    if config.verbose {
        info!("verbose logging enabled");
    }

    match config.mode {
        Mode::Client => {
            let client_cfg = config.client.expect("client config required");
            let client = Arc::new(Client::new(client_cfg.clone()));

            let listen_addr = client.clone().start().await?;
            info!(
                "client started listen={} servers={}",
                listen_addr,
                client_cfg.servers.len()
            );

            // Wait for shutdown signal
            wait_for_shutdown().await;

            info!("shutting down...");
            client.stop();
        }
        Mode::Server => {
            let server_cfg = config.server.expect("server config required");
            let server = Arc::new(Server::new(server_cfg.clone()));

            let listen_addrs = server.clone().start().await?;
            info!(
                "server started listen={} target={}",
                listen_addrs.len(),
                server_cfg.target_addr
            );

            // Wait for shutdown signal
            wait_for_shutdown().await;

            info!("shutting down...");
            server.stop();
        }
    }

    Ok(())
}

async fn wait_for_shutdown() {
    #[cfg(unix)]
    {
        let mut sigint = signal::unix::signal(signal::unix::SignalKind::interrupt())
            .expect("failed to install SIGINT handler");
        let mut sigterm = signal::unix::signal(signal::unix::SignalKind::terminate())
            .expect("failed to install SIGTERM handler");

        tokio::select! {
            _ = sigint.recv() => {}
            _ = sigterm.recv() => {}
        }
    }

    #[cfg(not(unix))]
    {
        signal::ctrl_c().await.expect("failed to listen for Ctrl+C");
    }
}
