//! Top-level app loop: protocol I/O, view dispatch, terminal lifecycle.
//!
//! Wire model:
//!   - Inherited stdin/stdout/stderr are the controlling TTY. Crossterm owns
//!     them for raw-mode keyboard input and alt-screen rendering.
//!   - JSON-RPC protocol uses two extra file descriptors passed by the Go
//!     parent via `cmd.ExtraFiles`:
//!       fd 3  →  inbound  (parent → child)
//!       fd 4  →  outbound (child  → parent)
//!     The fd numbers are configurable via OPSINTEL_TUI_PROTO_IN /
//!     OPSINTEL_TUI_PROTO_OUT env vars (defaulting to 3 and 4).
//!
//! When run standalone (no parent), the env vars are unset and we fall back
//! to stdin/stdout for protocol — handy for `tui-ping`-style headless tests.

use crate::protocol::{
    AgentDelta, AgentEnd, AgentError, DashboardSnapshot, DashboardState, DoctorSnapshot,
    DoctorState, Message, MonitorSnapshot, MonitorState, ReposSnapshot, ReposState, ViewPushParams,
    WizardPlan, WizardState, WizardStep,
};
use crate::views::dashboard::{DashboardOutbound, DashboardView};
use crate::views::doctor::{DoctorOutbound, DoctorView};
use crate::views::monitor::{MonitorOutbound, MonitorView};
use crate::views::repl::{Outbound, ReplView};
use crate::views::repos::{ReposOutbound, ReposView};
use crate::views::wizard::{WizardOutbound, WizardView};
use anyhow::{Context, Result};
use crossterm::{
    event::{self, DisableMouseCapture, EnableMouseCapture, Event, KeyCode, KeyEventKind},
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use ratatui::{backend::CrosstermBackend, Terminal};
use serde_json::{json, Value};
use std::fs::File;
use std::io::{self, BufRead, BufReader, Read, Write};
#[cfg(unix)]
use std::os::fd::FromRawFd;
use std::sync::mpsc::{self, Receiver, Sender};
use std::thread;
use std::time::Duration;

enum View {
    Empty,
    Repl(ReplView),
    Dashboard(DashboardView),
    Repos(ReposView),
    Doctor(DoctorView),
    Monitor(MonitorView),
    Wizard(WizardView),
}

pub fn run() -> Result<()> {
    // Install a panic hook that tears down the terminal cleanly before printing
    // the panic, so the message survives instead of being eaten by the
    // alt-screen leave. Without this, any panic in the render path (a JSON
    // decode error in handle_inbound, an out-of-bounds slice, …) looks like a
    // silent exit because the user only sees their shell prompt return.
    install_panic_hook();

    let (tx_in, rx_in) = mpsc::channel::<Message>();
    let (tx_out, rx_out) = mpsc::channel::<Message>();

    // Open the protocol channels. On Unix, use the inherited fds 3/4 (set by
    // the Go parent via ExtraFiles). When the env vars are unset (e.g. running
    // the binary standalone for debugging) fall back to stdin/stdout.
    let (proto_in, proto_out): (Box<dyn Read + Send>, Box<dyn Write + Send>) = open_protocol_streams()?;

    spawn_protocol_reader(proto_in, tx_in);
    spawn_protocol_writer(proto_out, rx_out);

    // Now crossterm can safely take over the inherited stdin/stdout TTY.
    enable_raw_mode().map_err(|e| {
        // Emit a hint to stderr so this isn't just a cryptic "Device not
        // configured (os error 6)" — that error means "the inherited stdin
        // isn't a TTY", which is almost always a bridge wiring issue.
        eprintln!(
            "opsintel-tui: enable_raw_mode failed: {e}\n  \
             stdin must be a TTY. If you launched via the Go bridge, ensure \
             bridge.go inherits the parent's controlling terminal (cmd.Stdin = os.Stdin)."
        );
        e
    }).context("enable raw mode")?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen, EnableMouseCapture)?;
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let _ = tx_out.send(Message::notification(
        "view.ready",
        json!({ "version": env!("CARGO_PKG_VERSION") }),
    ));

    let result = main_loop(&mut terminal, &rx_in, &tx_out);

    disable_raw_mode().ok();
    execute!(terminal.backend_mut(), LeaveAlternateScreen, DisableMouseCapture).ok();
    terminal.show_cursor().ok();
    result
}

/// Tear down the terminal before the default panic handler runs so the panic
/// message reaches the user's actual screen (not the soon-to-be-destroyed
/// alt-screen).
fn install_panic_hook() {
    let original = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        let _ = disable_raw_mode();
        let _ = execute!(
            io::stdout(),
            LeaveAlternateScreen,
            DisableMouseCapture
        );
        // Best-effort breadcrumb to the bridge log dir as well.
        if let Ok(dir) = std::env::var("OPSINTEL_TUI_LOG_DIR") {
            let path = std::path::Path::new(&dir).join("opsintel-tui-panic.log");
            if let Ok(mut f) = std::fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(&path)
            {
                use std::io::Write as _;
                let _ = writeln!(
                    f,
                    "{} panic: {}",
                    chrono_like_timestamp(),
                    info
                );
            }
        }
        original(info);
    }));
}

fn chrono_like_timestamp() -> String {
    // Avoid pulling in `chrono` just for this; std::time + a hand-written
    // format is enough for a panic breadcrumb.
    use std::time::{SystemTime, UNIX_EPOCH};
    let dur = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    format!("ts={}.{:03}", dur.as_secs(), dur.subsec_millis())
}

#[cfg(unix)]
fn open_protocol_streams() -> Result<(Box<dyn Read + Send>, Box<dyn Write + Send>)> {
    let in_fd = std::env::var("OPSINTEL_TUI_PROTO_IN")
        .ok()
        .and_then(|s| s.parse::<i32>().ok());
    let out_fd = std::env::var("OPSINTEL_TUI_PROTO_OUT")
        .ok()
        .and_then(|s| s.parse::<i32>().ok());
    match (in_fd, out_fd) {
        (Some(i), Some(o)) => {
            // SAFETY: fds 3/4 were inherited from the parent process and are
            // guaranteed valid by the bridge. We take ownership.
            let r: File = unsafe { File::from_raw_fd(i) };
            let w: File = unsafe { File::from_raw_fd(o) };
            Ok((Box::new(r), Box::new(w)))
        }
        _ => Ok((Box::new(io::stdin()), Box::new(io::stdout()))),
    }
}

#[cfg(not(unix))]
fn open_protocol_streams() -> Result<(Box<dyn Read + Send>, Box<dyn Write + Send>)> {
    // Windows: fallback to stdin/stdout (we don't currently support inherited
    // fds on Windows). The Go bridge already pipes stdin/stdout there; on
    // Windows the TUI runs in a "no-input-from-parent" mode anyway.
    Ok((Box::new(io::stdin()), Box::new(io::stdout())))
}

fn main_loop<B: ratatui::backend::Backend>(
    terminal: &mut Terminal<B>,
    rx_in: &Receiver<Message>,
    tx_out: &Sender<Message>,
) -> Result<()> {
    let mut view = View::Empty;
    let tick = Duration::from_millis(33);

    loop {
        loop {
            match rx_in.try_recv() {
                Ok(msg) => handle_inbound(&mut view, msg, tx_out),
                Err(mpsc::TryRecvError::Empty) => break,
                Err(mpsc::TryRecvError::Disconnected) => return Ok(()),
            }
        }

        terminal.draw(|f| {
            let area = f.area();
            match &mut view {
                View::Empty => render_empty(f, area),
                View::Repl(r) => {
                    r.set_size(area.width, area.height);
                    r.render(f, area);
                }
                View::Dashboard(d) => d.render(f, area),
                View::Repos(r) => r.render(f, area),
                View::Doctor(d) => d.render(f, area),
                View::Monitor(m) => m.render(f, area),
                View::Wizard(w) => w.render(f, area),
            }
        })?;

        if event::poll(tick)? {
            let ev = event::read()?;
            // Route mouse events to the active view (only wizard handles them
            // currently — the others ignore).
            if let Event::Mouse(me) = ev {
                if let View::Wizard(w) = &mut view {
                    match w.handle_mouse(me) {
                        WizardOutbound::Submit { id, fields, cancelled } => {
                            let _ = tx_out.send(Message::notification(
                                "wizard.submit",
                                json!({
                                    "id": id,
                                    "fields": fields,
                                    "cancelled": cancelled,
                                }),
                            ));
                        }
                        WizardOutbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        WizardOutbound::None => {}
                    }
                }
                continue;
            }
            if let Event::Key(key) = ev {
                if key.kind != KeyEventKind::Press {
                    continue;
                }
                match &mut view {
                    View::Repl(r) => match r.handle_key(key) {
                        Outbound::Submit(text) => {
                            let _ = tx_out.send(Message::notification(
                                "command.submit",
                                json!({ "text": text }),
                            ));
                        }
                        Outbound::Cancel => {
                            let _ = tx_out
                                .send(Message::notification("command.cancel", Value::Null));
                        }
                        Outbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        Outbound::None => {}
                    },
                    View::Wizard(w) => match w.handle_key(key) {
                        WizardOutbound::Submit { id, fields, cancelled } => {
                            let _ = tx_out.send(Message::notification(
                                "wizard.submit",
                                json!({
                                    "id": id,
                                    "fields": fields,
                                    "cancelled": cancelled,
                                }),
                            ));
                        }
                        WizardOutbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        WizardOutbound::None => {}
                    },
                    View::Doctor(d) => match d.handle_key(key) {
                        DoctorOutbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        DoctorOutbound::None => {}
                    },
                    View::Monitor(m) => match m.handle_key(key) {
                        MonitorOutbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        MonitorOutbound::None => {}
                    },
                    View::Dashboard(d) => match d.handle_key(key) {
                        DashboardOutbound::Dismiss => {
                            let _ = tx_out
                                .send(Message::notification("view.dismiss", Value::Null));
                            view = View::Empty;
                        }
                        DashboardOutbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        DashboardOutbound::Edit { yaml_path, value } => {
                            let _ = tx_out.send(Message::notification(
                                "dashboard.edit",
                                json!({
                                    "yaml_path": yaml_path,
                                    "value": value,
                                }),
                            ));
                        }
                        DashboardOutbound::None => {}
                    },
                    View::Repos(r) => match r.handle_key(key) {
                        ReposOutbound::Select(idx) => {
                            let _ = tx_out.send(Message::notification(
                                "repos.select",
                                json!({ "index": idx }),
                            ));
                        }
                        ReposOutbound::Sync(id) => {
                            let _ = tx_out.send(Message::notification(
                                "repos.sync",
                                json!({ "id": id }),
                            ));
                        }
                        ReposOutbound::Refresh => {
                            let _ = tx_out
                                .send(Message::notification("repos.refresh", Value::Null));
                        }
                        ReposOutbound::GraphSelect(id) => {
                            let _ = tx_out.send(Message::notification(
                                "repos.graph_select",
                                json!({ "id": id }),
                            ));
                        }
                        ReposOutbound::EditSubmit {
                            architecture,
                            review_hints,
                            user_context,
                        } => {
                            let _ = tx_out.send(Message::notification(
                                "repos.edit_submit",
                                json!({
                                    "architecture": architecture,
                                    "review_hints": review_hints,
                                    "user_context": user_context,
                                }),
                            ));
                        }
                        ReposOutbound::Quit => {
                            let _ = tx_out
                                .send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                        ReposOutbound::None => {}
                    },
                    View::Empty => {
                        if matches!(key.code, KeyCode::Char('q')) || matches!(key.code, KeyCode::Esc) {
                            let _ = tx_out.send(Message::notification("view.exit", Value::Null));
                            return Ok(());
                        }
                    }
                }
            }
        }
    }
}

fn render_empty(f: &mut ratatui::Frame, area: ratatui::layout::Rect) {
    use ratatui::{
        layout::Alignment,
        style::{Color, Modifier, Style},
        text::{Line, Span},
        widgets::{Block, Borders, Paragraph},
    };
    let body = Paragraph::new(vec![
        Line::raw(""),
        Line::from(Span::styled(
            "OpsIntelligence Rust TUI",
            Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD),
        )),
        Line::raw(""),
        Line::from(Span::styled(
            "waiting for view.push from host…",
            Style::default().fg(Color::DarkGray),
        )),
    ])
    .alignment(Alignment::Center)
    .block(Block::default().borders(Borders::ALL));
    f.render_widget(body, area);
}

fn handle_inbound(view: &mut View, msg: Message, tx_out: &Sender<Message>) {
    let method = msg.method.clone().unwrap_or_default();
    match method.as_str() {
        "ping" => {
            if let Some(id) = msg.id {
                let _ = tx_out.send(Message::response(
                    id,
                    json!({ "pong": true, "echo": msg.params }),
                ));
            }
        }
        "view.push" => {
            if let Some(params) = msg.params {
                if let Ok(p) = serde_json::from_value::<ViewPushParams>(params) {
                    match p.view.as_str() {
                        "repl" => {
                            *view = View::Repl(ReplView::new(p.repl.unwrap_or_default()));
                        }
                        "dashboard" => {
                            *view = View::Dashboard(DashboardView::new(
                                p.dashboard.unwrap_or_else(DashboardState::default),
                            ));
                        }
                        "repos" => {
                            *view = View::Repos(ReposView::new(
                                p.repos.unwrap_or_else(ReposState::default),
                            ));
                        }
                        "doctor" => {
                            *view = View::Doctor(DoctorView::new(
                                p.doctor.unwrap_or_else(DoctorState::default),
                            ));
                        }
                        "monitor" => {
                            *view = View::Monitor(MonitorView::new(
                                p.monitor.unwrap_or_else(MonitorState::default),
                            ));
                        }
                        "wizard" => {
                            *view = View::Wizard(WizardView::new(
                                p.wizard.unwrap_or_else(WizardState::default),
                            ));
                        }
                        _ => *view = View::Empty,
                    }
                }
            }
        }
        "agent.delta" => {
            if let View::Repl(r) = view {
                if let Some(params) = msg.params {
                    if let Ok(d) = serde_json::from_value::<AgentDelta>(params) {
                        r.apply_delta(d);
                    }
                }
            }
        }
        "agent.end" => {
            if let View::Repl(r) = view {
                let end = msg
                    .params
                    .and_then(|p| serde_json::from_value::<AgentEnd>(p).ok())
                    .unwrap_or_default();
                r.apply_end(end);
            }
        }
        "agent.error" => {
            if let View::Repl(r) = view {
                if let Some(p) = msg.params {
                    if let Ok(e) = serde_json::from_value::<AgentError>(p) {
                        r.apply_error(e);
                    }
                }
            }
        }
        "dashboard.snapshot" => {
            if let View::Dashboard(d) = view {
                if let Some(p) = msg.params {
                    if let Ok(snap) = serde_json::from_value::<DashboardSnapshot>(p) {
                        d.apply_snapshot(snap);
                    }
                }
            }
        }
        "dashboard.edit_result" => {
            if let View::Dashboard(d) = view {
                if let Some(p) = msg.params {
                    if let Ok(res) =
                        serde_json::from_value::<crate::protocol::DashboardEditResult>(p)
                    {
                        d.apply_edit_result(res);
                    }
                }
            }
        }
        "repos.snapshot" => {
            if let View::Repos(r) = view {
                if let Some(p) = msg.params {
                    if let Ok(snap) = serde_json::from_value::<ReposSnapshot>(p) {
                        r.apply_snapshot(snap);
                    }
                }
            }
        }
        "doctor.snapshot" => {
            if let View::Doctor(d) = view {
                if let Some(p) = msg.params {
                    if let Ok(snap) = serde_json::from_value::<DoctorSnapshot>(p) {
                        d.apply_snapshot(snap);
                    }
                }
            }
        }
        "monitor.snapshot" => {
            if let View::Monitor(m) = view {
                if let Some(p) = msg.params {
                    if let Ok(snap) = serde_json::from_value::<MonitorSnapshot>(p) {
                        m.apply_snapshot(snap);
                    }
                }
            }
        }
        "wizard.plan" => {
            if let View::Wizard(w) = view {
                if let Some(p) = msg.params {
                    if let Ok(plan) = serde_json::from_value::<WizardPlan>(p) {
                        w.apply_plan(plan);
                    }
                }
            }
        }
        "wizard.step" => {
            if let View::Wizard(w) = view {
                if let Some(p) = msg.params {
                    if let Ok(step) = serde_json::from_value::<WizardStep>(p) {
                        w.apply_step(step);
                    }
                }
            }
        }
        "exit" => {
            let _ = tx_out.send(Message::notification("view.exit", Value::Null));
        }
        _ => {}
    }
}

fn spawn_protocol_reader(reader: Box<dyn Read + Send>, tx: Sender<Message>) {
    thread::spawn(move || {
        let buf = BufReader::new(reader);
        for line in buf.lines() {
            let line = match line {
                Ok(l) => l,
                Err(_) => return,
            };
            if line.trim().is_empty() {
                continue;
            }
            match serde_json::from_str::<Message>(&line) {
                Ok(msg) => {
                    if tx.send(msg).is_err() {
                        return;
                    }
                }
                Err(e) => {
                    // Previously dropped silently — make malformed messages
                    // visible so we can debug protocol mismatches.
                    log_breadcrumb(&format!(
                        "protocol parse error: {} (line: {:.200})",
                        e, line
                    ));
                }
            }
        }
    });
}

fn log_breadcrumb(msg: &str) {
    if let Ok(dir) = std::env::var("OPSINTEL_TUI_LOG_DIR") {
        let path = std::path::Path::new(&dir).join("opsintel-tui-panic.log");
        if let Ok(mut f) = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
        {
            use std::io::Write as _;
            let _ = writeln!(f, "{} {}", chrono_like_timestamp(), msg);
        }
    }
}

fn spawn_protocol_writer(mut writer: Box<dyn Write + Send>, rx: Receiver<Message>) {
    thread::spawn(move || {
        for msg in rx.iter() {
            let line = match serde_json::to_string(&msg) {
                Ok(s) => s,
                Err(_) => continue,
            };
            if writeln!(writer, "{}", line).is_err() {
                return;
            }
            let _ = writer.flush();
        }
    });
}
