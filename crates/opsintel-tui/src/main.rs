mod app;
mod protocol;
mod theme;
mod views;
mod widgets;

use anyhow::{Context, Result};
use protocol::Message;
use serde_json::json;
use std::io::{self, Read, Write};

fn main() -> Result<()> {
    init_logging();

    let mut args = std::env::args().skip(1);
    if let Some(arg) = args.next() {
        if arg == "--headless" || arg == "--ping" {
            return run_headless();
        }
    }
    app::run()
}

fn init_logging() {
    if let Ok(dir) = std::env::var("OPSINTEL_TUI_LOG_DIR") {
        let appender = tracing_appender::rolling::never(&dir, "opsintel-tui.log");
        let _ = tracing_subscriber::fmt()
            .with_writer(appender)
            .with_env_filter(
                tracing_subscriber::EnvFilter::try_from_env("OPSINTEL_TUI_LOG")
                    .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
            )
            .try_init();
    }
}

/// Headless mode: read one JSON message from stdin, respond, exit.
fn run_headless() -> Result<()> {
    let mut line = String::new();
    io::stdin()
        .read_to_string(&mut line)
        .context("read stdin")?;
    // Take only the first line if multiple were sent.
    let line = line.lines().next().unwrap_or("").trim();
    if line.is_empty() {
        let reply = Message::notification("error", json!({ "message": "no input" }));
        let mut h = io::stdout().lock();
        writeln!(h, "{}", serde_json::to_string(&reply)?)?;
        return Ok(());
    }
    let msg: Message = serde_json::from_str(line).context("parse stdin message")?;
    let reply = match msg.method.as_deref() {
        Some("ping") => match msg.id {
            Some(id) => Message::response(id, json!({ "pong": true, "echo": msg.params })),
            None => Message::notification("pong", json!({ "echo": msg.params })),
        },
        Some(other) => match msg.id {
            Some(id) => Message::error(id, -32601, format!("method not found: {}", other)),
            None => Message::notification("error", json!({ "message": "unknown method" })),
        },
        None => Message::notification("error", json!({ "message": "missing method" })),
    };
    let mut h = io::stdout().lock();
    writeln!(h, "{}", serde_json::to_string(&reply)?)?;
    h.flush()?;
    Ok(())
}
