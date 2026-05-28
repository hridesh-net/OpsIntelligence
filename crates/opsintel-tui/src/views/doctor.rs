//! Doctor view — port of `cmd/opsintelligence/tui/doctor.go`.

use crate::protocol::{DoctorSnapshot, DoctorState};
use crate::theme;
use crate::widgets::spinner::Spinner;
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};

pub enum DoctorOutbound {
    None,
    Quit,
}

pub struct DoctorView {
    #[allow(dead_code)]
    pub state: DoctorState,
    pub snap: DoctorSnapshot,
    spinner: Spinner,
}

impl DoctorView {
    pub fn new(state: DoctorState) -> Self {
        Self {
            state,
            snap: DoctorSnapshot {
                running: true,
                checks: Vec::new(),
            },
            spinner: Spinner::new(),
        }
    }

    pub fn apply_snapshot(&mut self, snap: DoctorSnapshot) {
        self.snap = snap;
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> DoctorOutbound {
        if key.kind != KeyEventKind::Press {
            return DoctorOutbound::None;
        }
        match key.code {
            KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('Q') => DoctorOutbound::Quit,
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                DoctorOutbound::Quit
            }
            _ => DoctorOutbound::None,
        }
    }

    pub fn render(&self, f: &mut Frame, area: Rect) {
        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(2),
                Constraint::Min(3),
                Constraint::Length(1),
            ])
            .split(area);

        // Header.
        let header = if self.snap.running {
            Line::from(vec![
                Span::styled(
                    "OpsIntelligence Doctor",
                    theme::header(),
                ),
                Span::raw("  "),
                Span::styled(self.spinner.frame().to_string(), theme::neon()),
                Span::raw(" "),
                Span::styled("Running checks…", theme::muted()),
            ])
        } else {
            Line::from(vec![
                Span::styled(
                    "OpsIntelligence Doctor",
                    theme::header(),
                ),
                Span::raw("  "),
                Span::styled(
                    "✔ Checks complete",
                    Style::default()
                        .fg(theme::SUCCESS)
                        .add_modifier(Modifier::BOLD),
                ),
            ])
        };
        f.render_widget(Paragraph::new(header), layout[0]);

        // Body.
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(theme::PRIMARY));
        let inner = block.inner(layout[1]);
        f.render_widget(block, layout[1]);

        let mut lines: Vec<Line<'static>> = Vec::new();
        if self.snap.checks.is_empty() && self.snap.running {
            lines.push(Line::from(Span::styled("Initializing…", theme::muted())));
        }
        for c in &self.snap.checks {
            let (icon, icon_style, msg_style) = match c.severity.as_str() {
                "ok" => (
                    "✔ ",
                    Style::default()
                        .fg(theme::SUCCESS)
                        .add_modifier(Modifier::BOLD),
                    Style::default().fg(theme::ON_SURFACE),
                ),
                "warn" => (
                    "⚠ ",
                    Style::default()
                        .fg(theme::WARN)
                        .add_modifier(Modifier::BOLD),
                    Style::default().fg(theme::WARN),
                ),
                "error" => ("✗ ", theme::error_style(), theme::error_style()),
                "skipped" => ("⊘ ", theme::muted(), theme::muted()),
                _ => ("• ", theme::muted(), theme::muted()),
            };
            lines.push(Line::from(vec![
                Span::styled(icon, icon_style),
                Span::styled(format!("{:<20} ", c.id), theme::primary()),
                Span::styled(c.message.clone(), msg_style),
            ]));
        }
        f.render_widget(
            Paragraph::new(lines).wrap(Wrap { trim: false }),
            inner,
        );

        // Footer.
        let footer = if self.snap.running {
            "running…"
        } else {
            "Press q to quit"
        };
        f.render_widget(
            Paragraph::new(Span::styled(footer, theme::muted())),
            layout[2],
        );
    }
}
