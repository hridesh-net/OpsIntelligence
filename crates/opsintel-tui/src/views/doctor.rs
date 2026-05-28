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
        // Outer frame.
        let inner = crate::widgets::chrome::outer_block(
            f,
            area,
            "OpsIntelligence",
            "Doctor",
            None,
        );

        // Status pill: ok/warn/error breakdown when not running.
        if self.snap.running {
            let pill = format!(" {} running ", self.spinner.frame());
            crate::widgets::chrome::render_pill(f, area, &pill, theme::NEON);
        } else {
            let mut ok = 0u32;
            let mut warn = 0u32;
            let mut err = 0u32;
            for c in &self.snap.checks {
                match c.severity.as_str() {
                    "ok" => ok += 1,
                    "warn" => warn += 1,
                    "error" => err += 1,
                    _ => {}
                }
            }
            let (label, bg) = if err > 0 {
                (format!(" {} errors ", err), theme::ERROR_COLOR)
            } else if warn > 0 {
                (format!(" {} warnings ", warn), theme::WARN)
            } else {
                (format!(" {} ok ", ok), theme::SUCCESS)
            };
            crate::widgets::chrome::render_pill(f, area, &label, bg);
        }

        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([Constraint::Min(3), Constraint::Length(1)])
            .split(inner);

        // Body panel.
        let title = if self.snap.running {
            "Running checks…".to_string()
        } else {
            "Checks".to_string()
        };
        let body_block = crate::widgets::chrome::panel_block(&title, theme::PRIMARY, true);
        let body_inner = body_block.inner(layout[0]);
        f.render_widget(body_block, layout[0]);

        let mut lines: Vec<Line<'static>> = Vec::new();
        if self.snap.checks.is_empty() && self.snap.running {
            lines.push(Line::from(Span::styled(" Initializing…", theme::muted())));
        }
        for c in &self.snap.checks {
            let (dot, dot_style, msg_style) = match c.severity.as_str() {
                "ok" => (
                    "●",
                    Style::default()
                        .fg(theme::SUCCESS)
                        .add_modifier(Modifier::BOLD),
                    Style::default().fg(theme::ON_SURFACE),
                ),
                "warn" => (
                    "●",
                    Style::default()
                        .fg(theme::WARN)
                        .add_modifier(Modifier::BOLD),
                    Style::default().fg(theme::WARN),
                ),
                "error" => ("●", theme::error_style(), theme::error_style()),
                "skipped" => ("○", theme::muted(), theme::muted()),
                _ => ("·", theme::muted(), theme::muted()),
            };
            lines.push(Line::from(vec![
                Span::raw(" "),
                Span::styled(format!(" {} ", dot), dot_style),
                Span::styled(
                    format!("{:<20} ", c.id),
                    Style::default()
                        .fg(theme::PRIMARY)
                        .add_modifier(Modifier::BOLD),
                ),
                Span::styled(c.message.clone(), msg_style),
            ]));
        }
        f.render_widget(
            Paragraph::new(lines).wrap(Wrap { trim: false }),
            body_inner,
        );

        // Command bar.
        let entries: &[(&str, &str)] = if self.snap.running {
            &[("⌃C", "Abort")]
        } else {
            &[("Q", "Quit"), ("⎋", "Quit")]
        };
        crate::widgets::chrome::render_command_bar(f, layout[1], entries);
    }
}
