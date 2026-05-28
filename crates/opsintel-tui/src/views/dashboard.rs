//! Dashboard view — port of `cmd/opsintelligence/tui/dashboard.go`.
//!
//! Receives a `DashboardSnapshot` periodically from the Go side; this view is
//! responsible only for tab navigation, search filtering, and styling.

use crate::protocol::{
    AgentInfo, DashboardSnapshot, DashboardState, DashboardStatus, KeyValue, LogEntry,
};
use crate::theme;
use crate::widgets::textarea::{TextArea, TextAreaView};
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};

const TABS: &[&str] = &["Status", "Config", "Limits", "Usage", "Agents", "Logs"];

pub enum DashboardOutbound {
    None,
    /// Esc/q in overlay mode: just dismiss this view, host stays.
    Dismiss,
    /// Esc/q in standalone mode: quit the program.
    Quit,
}

pub struct DashboardView {
    pub state: DashboardState,
    pub snap: DashboardSnapshot,
    active_tab: usize,
    search: TextArea,
    search_active: bool,
    scroll: u16,
}

impl DashboardView {
    pub fn new(state: DashboardState) -> Self {
        Self {
            state,
            snap: DashboardSnapshot::default(),
            active_tab: 0,
            search: TextArea::new("Search settings…"),
            search_active: false,
            scroll: 0,
        }
    }

    pub fn apply_snapshot(&mut self, snap: DashboardSnapshot) {
        self.snap = snap;
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> DashboardOutbound {
        if key.kind != KeyEventKind::Press {
            return DashboardOutbound::None;
        }
        if self.search_active {
            match key.code {
                KeyCode::Esc => {
                    self.search_active = false;
                    self.search.reset();
                    return DashboardOutbound::None;
                }
                KeyCode::Enter => {
                    self.search_active = false;
                    return DashboardOutbound::None;
                }
                _ => {
                    let _ = self.search.handle_key(key);
                    return DashboardOutbound::None;
                }
            }
        }
        match key.code {
            KeyCode::Char('/') => {
                self.search_active = true;
                return DashboardOutbound::None;
            }
            KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('Q') => {
                return if self.state.overlay {
                    DashboardOutbound::Dismiss
                } else {
                    DashboardOutbound::Quit
                };
            }
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return DashboardOutbound::Quit;
            }
            KeyCode::Left | KeyCode::BackTab => {
                self.active_tab = (self.active_tab + TABS.len() - 1) % TABS.len();
                self.scroll = 0;
            }
            KeyCode::Right | KeyCode::Tab => {
                self.active_tab = (self.active_tab + 1) % TABS.len();
                self.scroll = 0;
            }
            KeyCode::Up => self.scroll = self.scroll.saturating_sub(1),
            KeyCode::Down => self.scroll = self.scroll.saturating_add(1),
            KeyCode::PageUp => self.scroll = self.scroll.saturating_sub(10),
            KeyCode::PageDown => self.scroll = self.scroll.saturating_add(10),
            _ => {}
        }
        DashboardOutbound::None
    }

    pub fn render(&mut self, f: &mut Frame, area: Rect) {
        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1), // context strip
                Constraint::Length(1), // tab row
                Constraint::Length(3), // search bar
                Constraint::Length(1), // divider
                Constraint::Min(3),    // body
                Constraint::Length(1), // footer
            ])
            .split(area);

        // Context strip.
        let strip = Paragraph::new(Line::from(vec![
            Span::styled("› ", Style::default().fg(theme::PRIMARY)),
            Span::styled(self.state.context_label.clone(), Style::default().fg(theme::ON_SURFACE)),
        ]))
        .style(Style::default().bg(theme::CHROME_BG));
        f.render_widget(strip, layout[0]);

        // Tab row.
        let mut tab_spans: Vec<Span> = Vec::new();
        for (i, t) in TABS.iter().enumerate() {
            let style = if i == self.active_tab {
                Style::default().fg(theme::PRIMARY).add_modifier(Modifier::BOLD)
            } else {
                theme::muted()
            };
            if i > 0 {
                tab_spans.push(Span::raw("  "));
            }
            tab_spans.push(Span::styled(format!(" {} ", t), style));
        }
        let tabs = Paragraph::new(Line::from(tab_spans));
        f.render_widget(tabs, layout[1]);

        // Search bar.
        let search_border = if self.search_active {
            Style::default().fg(theme::PRIMARY)
        } else {
            Style::default().fg(theme::BORDER)
        };
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(search_border);
        let search_area = layout[2];
        let inner = block.inner(search_area);
        f.render_widget(block, search_area);
        let ta = TextAreaView {
            area: &self.search,
            style: Style::default().fg(theme::ON_SURFACE),
            placeholder_style: theme::muted(),
            focused: self.search_active,
        };
        f.render_widget(ta, inner);

        // Divider.
        let div = Paragraph::new(Span::styled(
            "─".repeat(layout[3].width as usize),
            Style::default().fg(theme::BORDER),
        ));
        f.render_widget(div, layout[3]);

        // Body — dispatch per tab.
        let query = self.search.value().to_ascii_lowercase();
        let q = query.trim();
        let lines = match self.active_tab {
            0 => render_status(&self.snap.status, q),
            1 => render_kv(&self.snap.config, "Configuration", q),
            2 => render_kv(&self.snap.limits, "Limits", q),
            3 => render_usage(&self.snap.usage, &self.snap.usage_empty_hint, q),
            4 => render_agents(&self.snap.agents, q),
            5 => render_logs(&self.snap.logs, &self.snap.log_source_path, q),
            _ => Vec::new(),
        };
        let max_scroll = (lines.len() as u16).saturating_sub(layout[4].height);
        if self.scroll > max_scroll {
            self.scroll = max_scroll;
        }
        let body = Paragraph::new(lines)
            .wrap(Wrap { trim: false })
            .scroll((self.scroll, 0));
        f.render_widget(body, layout[4]);

        // Footer.
        let hint = if self.state.overlay {
            "← → tab to switch  ·  / search  ·  Esc close"
        } else {
            "← → tab to switch  ·  / search  ·  q / Esc quit"
        };
        let footer = Paragraph::new(Span::styled(hint, theme::muted()));
        f.render_widget(footer, layout[5]);
    }
}

fn render_status(st: &DashboardStatus, query: &str) -> Vec<Line<'static>> {
    let mut lines: Vec<Line<'static>> = vec![Line::from(Span::styled(
        "Orchestrator",
        theme::header(),
    ))];
    lines.push(Line::raw(""));

    let status_line = if st.alive {
        Line::from(vec![
            Span::styled("● ", Style::default().fg(theme::SUCCESS).add_modifier(Modifier::BOLD)),
            Span::styled("RUNNING", Style::default().fg(theme::PRIMARY).add_modifier(Modifier::BOLD)),
            Span::raw("  "),
            Span::styled(format!("PID {}", st.pid), theme::muted()),
            Span::raw("  "),
            Span::styled(st.etime.clone(), theme::muted()),
        ])
    } else {
        Line::from(vec![
            Span::styled("● ", theme::error_style()),
            Span::styled("STOPPED", theme::error_style()),
        ])
    };
    lines.push(status_line);
    lines.push(Line::raw(""));

    push_kv(&mut lines, "version", &st.version);
    push_kv(&mut lines, "skills", &st.skill_summary);
    lines.push(Line::raw(""));
    lines.push(progress_line("CPU", st.cpu_percent, &format!("{:.1}%", st.cpu_percent)));
    lines.push(progress_line(
        "RAM",
        (st.rss_mb / 1024.0 * 100.0).min(100.0),
        &format!("{:.1} MB", st.rss_mb),
    ));
    lines.push(Line::raw(""));

    let channels = if st.channels.is_empty() {
        "none".to_string()
    } else {
        st.channels.join(" · ")
    };
    push_kv(&mut lines, "channels", &channels);
    push_kv(
        &mut lines,
        "plano",
        &toggle_text(&st.plano.enabled, &st.plano.detail),
    );
    push_kv(
        &mut lines,
        "mcp",
        &toggle_text(&st.mcp.enabled, &st.mcp.detail),
    );

    if !st.gateway_base.is_empty() {
        lines.push(Line::raw(""));
        push_kv(&mut lines, "dashboard", &format!("{}/dashboard/", st.gateway_base.trim_end_matches('/')));
        push_kv(&mut lines, "health", &format!("curl -sS {}/health", st.gateway_base.trim_end_matches('/')));
        if !st.gateway_bind.is_empty() && st.gateway_bind != "loopback" {
            push_kv(&mut lines, "gateway.bind", &st.gateway_bind);
        }
    }
    if !st.run_trace_file.is_empty() {
        lines.push(Line::raw(""));
        push_kv(&mut lines, "run trace", &format!("tail -f {}", st.run_trace_file));
    }

    filter_lines(lines, query)
}

fn render_kv(rows: &[KeyValue], title: &str, query: &str) -> Vec<Line<'static>> {
    let mut lines: Vec<Line<'static>> = vec![Line::from(Span::styled(
        title.to_string(),
        theme::header(),
    ))];
    lines.push(Line::raw(""));
    let mut matched = 0;
    for r in rows {
        if !query.is_empty()
            && !r.k.to_ascii_lowercase().contains(query)
            && !r.v.to_ascii_lowercase().contains(query)
        {
            continue;
        }
        matched += 1;
        push_kv(&mut lines, &r.k, &r.v);
    }
    if !query.is_empty() && matched == 0 {
        lines.push(Line::from(Span::styled(
            format!("No matches for \"{}\"", query),
            theme::muted(),
        )));
    }
    lines
}

fn render_usage(rows: &[KeyValue], empty_hint: &str, query: &str) -> Vec<Line<'static>> {
    let mut lines: Vec<Line<'static>> = vec![Line::from(Span::styled("Usage", theme::header()))];
    lines.push(Line::raw(""));
    if rows.is_empty() {
        lines.push(Line::from(Span::styled(empty_hint.to_string(), theme::muted())));
        return lines;
    }
    let mut matched = 0;
    for r in rows {
        if !query.is_empty()
            && !r.k.to_ascii_lowercase().contains(query)
            && !r.v.to_ascii_lowercase().contains(query)
        {
            continue;
        }
        matched += 1;
        push_kv(&mut lines, &r.k, &r.v);
    }
    if !query.is_empty() && matched == 0 {
        lines.push(Line::from(Span::styled(
            format!("No matches for \"{}\"", query),
            theme::muted(),
        )));
    }
    lines
}

fn render_agents(agents: &[AgentInfo], query: &str) -> Vec<Line<'static>> {
    let mut lines: Vec<Line<'static>> = vec![Line::from(Span::styled(
        "Active Agents",
        theme::header(),
    ))];
    lines.push(Line::raw(""));
    lines.push(Line::from(vec![
        Span::styled("■ master  ", Style::default().fg(theme::SUCCESS)),
        Span::styled("running", theme::muted()),
    ]));

    let active: Vec<&AgentInfo> = agents
        .iter()
        .filter(|a| matches!(a.status.as_str(), "running" | "pending"))
        .collect();
    if active.is_empty() && query.is_empty() {
        lines.push(Line::from(Span::styled("  (no active specialists)", theme::muted())));
    }
    for a in &active {
        if !query.is_empty()
            && !a.name.to_ascii_lowercase().contains(query)
            && !a.status.contains(query)
        {
            continue;
        }
        let (icon, sty) = match a.status.as_str() {
            "pending" => ("◷", Style::default().fg(theme::NEON)),
            _ => ("■", Style::default().fg(theme::SUCCESS)),
        };
        lines.push(Line::from(vec![
            Span::raw("  └─ "),
            Span::styled(icon, sty),
            Span::raw(" "),
            Span::styled(a.name.clone(), Style::default().add_modifier(Modifier::BOLD)),
            Span::raw("  "),
            Span::styled(a.status.clone(), sty),
            Span::raw("  "),
            Span::styled(a.elapsed.clone(), theme::muted()),
        ]));
        if !a.last_message.is_empty() {
            let phase = if a.last_phase.is_empty() { "event" } else { &a.last_phase };
            lines.push(Line::from(vec![
                Span::raw("       "),
                Span::styled(format!("[{}] ", phase), theme::muted()),
                Span::styled(a.last_message.clone(), theme::muted()),
            ]));
        }
    }

    let done: Vec<&AgentInfo> = agents
        .iter()
        .filter(|a| matches!(a.status.as_str(), "completed" | "failed" | "cancelled"))
        .collect();
    if !done.is_empty() {
        lines.push(Line::raw(""));
        lines.push(Line::from(Span::styled(
            "── Completed ─────────────────────────────────",
            theme::muted(),
        )));
        for a in &done {
            if !query.is_empty()
                && !a.name.to_ascii_lowercase().contains(query)
                && !a.status.contains(query)
            {
                continue;
            }
            let (icon, sty) = match a.status.as_str() {
                "failed" => ("✗", theme::error_style()),
                "cancelled" => ("⊘", theme::muted()),
                _ => ("✓", Style::default().fg(theme::SUCCESS)),
            };
            let mut row = vec![
                Span::styled(icon, sty),
                Span::raw(" "),
                Span::styled(a.name.clone(), Style::default().add_modifier(Modifier::BOLD)),
                Span::raw("  "),
                Span::styled(a.status.clone(), theme::muted()),
                Span::raw("  "),
                Span::styled(a.elapsed.clone(), theme::muted()),
            ];
            if !a.error.is_empty() {
                row.push(Span::raw("  "));
                row.push(Span::styled(a.error.clone(), theme::error_style()));
            }
            lines.push(Line::from(row));
        }
    }
    if active.is_empty() && done.is_empty() {
        lines.push(Line::raw(""));
        lines.push(Line::from(Span::styled(
            "No specialist agents have been spawned yet in this session.",
            theme::muted(),
        )));
    }
    lines
}

fn render_logs(logs: &[LogEntry], path: &str, query: &str) -> Vec<Line<'static>> {
    let mut lines: Vec<Line<'static>> = vec![Line::from(vec![
        Span::styled("Run Trace", theme::header()),
        Span::raw("  "),
        Span::styled(path.to_string(), theme::muted()),
    ])];
    lines.push(Line::raw(""));
    lines.push(Line::from(Span::styled(
        format!("{:<8}  {:<12}  {:<18}  DETAIL", "TIME", "ROLE/SOURCE", "EVENT"),
        theme::muted(),
    )));
    lines.push(Line::from(Span::styled(
        "─".repeat(70),
        theme::muted(),
    )));

    if logs.is_empty() {
        lines.push(Line::from(Span::styled(
            "No trace events yet. Send a message to the agent to start.",
            theme::muted(),
        )));
        return lines;
    }

    for e in logs {
        let needle = format!(
            "{} {} {}",
            e.kind.to_ascii_lowercase(),
            e.source.to_ascii_lowercase(),
            e.detail.to_ascii_lowercase()
        );
        if !query.is_empty() && !needle.contains(query) {
            continue;
        }
        let source_style = if e.source == "master" || e.source.starts_with("repointel") {
            Style::default().fg(theme::SUCCESS)
        } else if e.source.starts_with("sub") {
            Style::default().fg(theme::PRIMARY)
        } else {
            Style::default().fg(theme::NEON)
        };
        let kind_style = if e.error {
            theme::error_style()
        } else {
            Style::default().fg(theme::ON_SURFACE)
        };
        lines.push(Line::from(vec![
            Span::styled(format!("{:<8}", e.time), theme::muted()),
            Span::raw("  "),
            Span::styled(format!("{:<12}", truncate(&e.source, 12)), source_style),
            Span::raw("  "),
            Span::styled(format!("{:<18}", truncate(&e.kind, 18)), kind_style),
            Span::raw("  "),
            Span::styled(truncate(&e.detail, 60), theme::muted()),
        ]));
    }
    lines
}

fn push_kv(out: &mut Vec<Line<'static>>, k: &str, v: &str) {
    const KEY_W: usize = 22;
    let pad = if k.len() < KEY_W {
        " ".repeat(KEY_W - k.len())
    } else {
        " ".to_string()
    };
    out.push(Line::from(vec![
        Span::styled(format!("{}{}", k, pad), theme::muted()),
        Span::styled(v.to_string(), Style::default().fg(theme::PRIMARY)),
    ]));
}

fn progress_line(label: &str, pct: f32, value: &str) -> Line<'static> {
    let width = 14usize;
    let filled = ((pct.max(0.0).min(100.0) / 100.0) * width as f32) as usize;
    let mut bar = String::new();
    for i in 0..width {
        bar.push(if i < filled { '█' } else { '░' });
    }
    Line::from(vec![
        Span::styled(format!("  {} ", label), theme::muted()),
        Span::styled(bar, Style::default().fg(theme::PRIMARY)),
        Span::styled(format!("  {}", value), theme::muted()),
    ])
}

fn toggle_text(enabled: &bool, detail: &str) -> String {
    if !enabled {
        return "disabled".to_string();
    }
    if detail.is_empty() {
        return "✓".to_string();
    }
    format!("✓ {}", detail)
}

fn truncate(s: &str, max: usize) -> String {
    let chars: Vec<char> = s.chars().collect();
    if chars.len() <= max {
        return s.to_string();
    }
    let mut t: String = chars.into_iter().take(max.saturating_sub(1)).collect();
    t.push('…');
    t
}

fn filter_lines(lines: Vec<Line<'static>>, query: &str) -> Vec<Line<'static>> {
    if query.is_empty() {
        return lines;
    }
    let mut out: Vec<Line<'static>> = Vec::new();
    let mut matched = 0;
    for ln in lines.into_iter() {
        let txt = ln
            .spans
            .iter()
            .map(|s| s.content.as_ref())
            .collect::<Vec<_>>()
            .join("")
            .to_ascii_lowercase();
        if txt.contains(query) {
            out.push(ln);
            matched += 1;
        }
    }
    if matched == 0 {
        out.push(Line::from(Span::styled(
            format!("No matches for \"{}\"", query),
            theme::muted(),
        )));
    }
    out
}
