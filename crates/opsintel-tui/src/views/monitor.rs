//! Monitor view — port of `cmd/opsintelligence/tui/monitor.go`.

use crate::protocol::{MonitorEvent, MonitorSnapshot, MonitorState};
use crate::theme;
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Paragraph, Wrap},
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
        // Outer frame.
        let inner = crate::widgets::chrome::outer_block(
            f,
            area,
            "OpsIntelligence",
            "Monitor",
            None,
        );
        // Status pill.
        let st = &self.snap.status;
        let (pill, pill_bg) = if st.alive {
            (format!(" PID {}  ·  {} ", st.pid, st.etime), theme::SUCCESS)
        } else {
            (" STOPPED ".to_string(), theme::ERROR_COLOR)
        };
        crate::widgets::chrome::render_pill(f, area, &pill, pill_bg);

        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(4), // resources panel (CPU/RAM + version)
                Constraint::Min(3),    // events panel
                Constraint::Length(1), // command bar
            ])
            .split(inner);

        // Resources panel.
        let res_block = crate::widgets::chrome::panel_block(
            "Resources",
            theme::PRIMARY,
            true,
        );
        let res_inner = res_block.inner(layout[0]);
        f.render_widget(res_block, layout[0]);
        let resource_lines = vec![
            Line::from(vec![
                Span::raw(" "),
                Span::styled("CPU ", theme::muted()),
                Span::styled(progress_bar(st.cpu_percent, 14), Style::default().fg(theme::PRIMARY)),
                Span::styled(format!("  {:.1}%   ", st.cpu_percent), theme::muted()),
                Span::styled("RAM ", theme::muted()),
                Span::styled(
                    progress_bar((st.rss_mb / 1024.0 * 100.0).min(100.0), 14),
                    Style::default().fg(theme::PRIMARY),
                ),
                Span::styled(format!("  {:.0} MB", st.rss_mb), theme::muted()),
            ]),
            Line::from(vec![
                Span::raw(" "),
                Span::styled("version ", theme::muted()),
                Span::styled(
                    self.state.version.clone(),
                    Style::default().fg(theme::ON_SURFACE),
                ),
            ]),
        ];
        f.render_widget(Paragraph::new(resource_lines), res_inner);

        // Live Run Trace panel.
        let trace_block = crate::widgets::chrome::panel_block(
            "Live Run Trace",
            theme::PRIMARY,
            true,
        );
        let trace_inner = trace_block.inner(layout[1]);
        f.render_widget(trace_block, layout[1]);

        let mut lines: Vec<Line<'static>> = vec![
            Line::from(Span::styled(
                format!(" {:<10}{:<6}{:<40}{}", "TIME", "ITER", "EVENT", "TOOL/CHAIN"),
                Style::default()
                    .fg(theme::ON_SURFACE)
                    .add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled(
                "─".repeat(trace_inner.width as usize),
                theme::muted(),
            )),
        ];
        for ev in &self.snap.events {
            lines.push(event_line(ev));
        }
        if self.snap.events.is_empty() {
            lines.push(Line::from(Span::styled(
                " No log events yet — waiting for the orchestrator to write to the log.",
                theme::muted(),
            )));
        }
        f.render_widget(
            Paragraph::new(lines).wrap(Wrap { trim: false }),
            trace_inner,
        );

        // Command bar.
        crate::widgets::chrome::render_command_bar(
            f,
            layout[2],
            &[("Q", "Quit"), ("⎋", "Quit")],
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
        Span::raw(" "),
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
