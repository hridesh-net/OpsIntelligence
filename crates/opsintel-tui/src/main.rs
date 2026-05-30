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

/// Headless mode: read one JSON message from the inherited protocol fd
/// (`OPSINTEL_TUI_PROTO_IN`, set by the Go bridge) when present, falling back
/// to plain stdin for standalone debugging. Reply on the matching out fd or
/// stdout. This keeps `opsintelligence tui-ping` working both under the Go
/// bridge (which uses fds 3/4 for protocol) AND in a headless GHA runner
/// where stdin is /dev/null.
fn run_headless() -> Result<()> {
    use std::io::BufRead;
    #[cfg(unix)]
    use std::os::fd::FromRawFd;

    let (in_fd, out_fd) = (
        std::env::var("OPSINTEL_TUI_PROTO_IN").ok().and_then(|s| s.parse::<i32>().ok()),
        std::env::var("OPSINTEL_TUI_PROTO_OUT").ok().and_then(|s| s.parse::<i32>().ok()),
    );

    let mut line = String::new();
    #[cfg(unix)]
    if let (Some(i), Some(_o)) = (in_fd, out_fd) {
        // SAFETY: fds 3/4 were inherited from the Go parent.
        let f: std::fs::File = unsafe { std::fs::File::from_raw_fd(i) };
        let mut br = std::io::BufReader::new(f);
        br.read_line(&mut line).context("read protocol fd")?;
    } else {
        io::stdin().read_to_string(&mut line).context("read stdin")?;
    }
    #[cfg(not(unix))]
    {
        io::stdin().read_to_string(&mut line).context("read stdin")?;
    }
    // suppress unused warning when env var path is taken
    let _ = (in_fd, out_fd);
    // Take only the first line if multiple were sent.
    let line = line.lines().next().unwrap_or("").trim();
    if line.is_empty() {
        let reply = Message::notification("error", json!({ "message": "no input" }));
        write_headless_reply(out_fd, &reply)?;
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
    write_headless_reply(out_fd, &reply)
}

fn write_headless_reply(out_fd: Option<i32>, reply: &Message) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::fd::FromRawFd;
        if let Some(o) = out_fd {
            // SAFETY: fd 4 is the inherited rust→go pipe write-end.
            let mut f: std::fs::File = unsafe { std::fs::File::from_raw_fd(o) };
            writeln!(f, "{}", serde_json::to_string(reply)?)?;
            f.flush()?;
            return Ok(());
        }
    }
    let _ = out_fd;
    let mut h = io::stdout().lock();
    writeln!(h, "{}", serde_json::to_string(reply)?)?;
    h.flush()?;
    Ok(())
}
