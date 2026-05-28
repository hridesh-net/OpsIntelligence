//! Monitor view — port of `cmd/opsintelligence/tui/monitor.go`.

use crate::protocol::{MonitorEvent, MonitorSnapshot, MonitorState};
use crate::theme;
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};

pub enum MonitorOutbound {
    None,
    Quit,
}

pub struct MonitorView {
    pub state: MonitorState,
    pub snap: MonitorSnapshot,
}

impl MonitorView {
    pub fn new(state: MonitorState) -> Self {
        Self {
            state,
            snap: MonitorSnapshot::default(),
        }
    }

    pub fn apply_snapshot(&mut self, snap: MonitorSnapshot) {
        self.snap = snap;
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> MonitorOutbound {
        if key.kind != KeyEventKind::Press {
            return MonitorOutbound::None;
        }
        match key.code {
            KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('Q') => MonitorOutbound::Quit,
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                MonitorOutbound::Quit
            }
            _ => MonitorOutbound::None,
        }
    }

    pub fn render(&self, f: &mut Frame, area: Rect) {
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(theme::PRIMARY));
        let inner = block.inner(area);
        f.render_widget(block, area);

        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1), // status line
                Constraint::Length(1), // spacer
                Constraint::Length(1), // CPU/RAM
                Constraint::Length(1), // spacer
                Constraint::Length(1), // header
                Constraint::Min(3),    // event table
                Constraint::Length(1), // footer
            ])
            .split(inner);

        // Status line.
        let st = &self.snap.status;
        let status_line = if st.alive {
            Line::from(vec![
                Span::styled(
                    "● ",
                    Style::default().fg(theme::SUCCESS).add_modifier(Modifier::BOLD),
                ),
                Span::styled("MONITORING", theme::primary()),
                Span::raw(" "),
                Span::styled(self.state.version.clone(), theme::muted()),
                Span::raw("  "),
                Span::styled(format!("PID {}", st.pid), theme::muted()),
                Span::raw("  "),
                Span::styled(st.etime.clone(), theme::muted()),
            ])
        } else {
            Line::from(vec![
                Span::styled("● ", theme::error_style()),
                Span::styled("ORCHESTRATOR STOPPED", theme::error_style()),
            ])
        };
        f.render_widget(Paragraph::new(status_line), layout[0]);

        // CPU/RAM bars.
        let bars = Line::from(vec![
            Span::styled("CPU ", theme::muted()),
            Span::styled(progress_bar(st.cpu_percent, 10), Style::default().fg(theme::PRIMARY)),
            Span::styled(format!(" {:.1}%   ", st.cpu_percent), theme::muted()),
            Span::styled("RAM ", theme::muted()),
            Span::styled(
                progress_bar((st.rss_mb / 1024.0 * 100.0).min(100.0), 10),
                Style::default().fg(theme::PRIMARY),
            ),
            Span::styled(format!(" {:.0} MB", st.rss_mb), theme::muted()),
        ]);
        f.render_widget(Paragraph::new(bars), layout[2]);

        // Section header.
        f.render_widget(
            Paragraph::new(Span::styled("Live Run Trace", theme::header())),
            layout[4],
        );

        // Event table.
        let mut lines: Vec<Line<'static>> = vec![
            Line::from(Span::styled(
                format!("{:<10}{:<6}{:<40}{}", "TIME", "ITER", "EVENT", "TOOL/CHAIN"),
                theme::header(),
            )),
            Line::from(Span::styled("─".repeat(76), theme::muted())),
        ];
        for ev in &self.snap.events {
            lines.push(event_line(ev));
        }
        if self.snap.events.is_empty() {
            lines.push(Line::from(Span::styled(
                "No log events yet — waiting for the orchestrator to write to the configured log file.",
                theme::muted(),
            )));
        }
        f.render_widget(
            Paragraph::new(lines).wrap(Wrap { trim: false }),
            layout[5],
        );

        // Footer.
        f.render_widget(
            Paragraph::new(Span::styled("q to quit", theme::muted())),
            layout[6],
        );
    }
}

fn event_line(ev: &MonitorEvent) -> Line<'static> {
    let iter_str = if ev.iteration == 0 {
        "-".to_string()
    } else {
        ev.iteration.to_string()
    };
    Line::from(vec![
        Span::styled(format!("{:<10}", ev.time), theme::muted()),
        Span::styled(format!("{:<6}", iter_str), theme::muted()),
        Span::styled(format!("{:<40}", truncate(&ev.message, 40)), Style::default()),
        Span::styled(truncate(&ev.tool, 20), Style::default().fg(theme::PRIMARY)),
    ])
}

fn progress_bar(pct: f32, width: usize) -> String {
    let p = pct.max(0.0).min(100.0);
    let filled = ((p / 100.0) * width as f32) as usize;
    let mut bar = String::with_capacity(width);
    for i in 0..width {
        bar.push(if i < filled { '█' } else { '░' });
    }
    bar
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
