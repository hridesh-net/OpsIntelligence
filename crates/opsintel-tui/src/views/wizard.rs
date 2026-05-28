//! Wizard view — generic multi-step form renderer that ports the legacy
//! `cmd/opsintelligence/tui/onboard_*.go` Bubbletea program.
//!
//! The wizard is driven entirely by the host: Go sends one `wizard.step` at a
//! time (either a `Form` or a `Side`). When the user completes a form, Rust
//! sends `wizard.submit` and waits for the next push.

use crate::protocol::{WizardField, WizardForm, WizardOption, WizardSide, WizardState, WizardStep};
use crate::theme;
use crate::widgets::spinner::Spinner;
use crate::widgets::textarea::{TextArea, TextAreaView};
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};
use serde_json::{json, Value};

pub enum WizardOutbound {
    None,
    Submit { id: u64, fields: Value, cancelled: bool },
    Quit,
}

enum ActiveStep {
    None,
    Form(FormState),
    Side(SideState),
    Done(String),
}

struct FormState {
    spec: WizardForm,
    /// Per-field editing state, indexed by field position.
    fields: Vec<FieldRuntime>,
    cursor: usize,
}

enum FieldRuntime {
    Input(TextArea),
    Select {
        index: usize,
    },
    MultiSelect {
        selected: Vec<bool>,
        cursor: usize,
    },
    Confirm {
        value: bool,
    },
    Note,
}

struct SideState {
    spec: WizardSide,
    spinner: Spinner,
}

pub struct WizardView {
    state: WizardState,
    active: ActiveStep,
}

impl WizardView {
    pub fn new(state: WizardState) -> Self {
        Self {
            state,
            active: ActiveStep::None,
        }
    }

    pub fn apply_step(&mut self, step: WizardStep) {
        self.active = match step {
            WizardStep::Form(spec) => {
                let fields = spec
                    .fields
                    .iter()
                    .map(make_runtime)
                    .collect::<Vec<_>>();
                let cursor = first_interactive_field(&fields).unwrap_or(0);
                ActiveStep::Form(FormState {
                    spec,
                    fields,
                    cursor,
                })
            }
            WizardStep::Side(spec) => ActiveStep::Side(SideState {
                spec,
                spinner: Spinner::new(),
            }),
            WizardStep::Done { message } => ActiveStep::Done(message),
        };
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> WizardOutbound {
        if key.kind != KeyEventKind::Press {
            return WizardOutbound::None;
        }
        if matches!(key.code, KeyCode::Char('c'))
            && key.modifiers.contains(KeyModifiers::CONTROL)
        {
            return WizardOutbound::Quit;
        }
        match &mut self.active {
            ActiveStep::Form(f) => handle_form_key(f, key),
            ActiveStep::Side(_) | ActiveStep::None => WizardOutbound::None,
            ActiveStep::Done(_) => {
                if matches!(key.code, KeyCode::Enter | KeyCode::Esc | KeyCode::Char('q')) {
                    WizardOutbound::Quit
                } else {
                    WizardOutbound::None
                }
            }
        }
    }

    pub fn render(&mut self, f: &mut Frame, area: Rect) {
        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(3), // header
                Constraint::Min(3),    // body
                Constraint::Length(1), // footer
            ])
            .split(area);

        self.render_header(f, layout[0]);
        match &mut self.active {
            ActiveStep::None => {
                f.render_widget(
                    Paragraph::new(Span::styled("Waiting for first step…", theme::muted())),
                    layout[1],
                );
            }
            ActiveStep::Form(state) => render_form(f, layout[1], state),
            ActiveStep::Side(state) => render_side(f, layout[1], state),
            ActiveStep::Done(msg) => render_done(f, layout[1], msg),
        }
        let hint = match &self.active {
            ActiveStep::Form(_) => {
                "Tab/↓ next field · Shift+Tab/↑ prev · Enter submit · Ctrl+C quit"
            }
            ActiveStep::Side(_) => "Working…  ·  Ctrl+C to abort",
            ActiveStep::Done(_) => "Press Enter or q to exit",
            ActiveStep::None => " ",
        };
        f.render_widget(
            Paragraph::new(Span::styled(hint, theme::muted())),
            layout[2],
        );
    }

    fn render_header(&self, f: &mut Frame, area: Rect) {
        let (step_num, step_total, icon, title, subtitle) = match &self.active {
            ActiveStep::Form(s) => (
                s.spec.step_num,
                s.spec.step_total,
                s.spec.icon.as_str(),
                s.spec.title.as_str(),
                s.spec.subtitle.as_str(),
            ),
            ActiveStep::Side(s) => (
                s.spec.step_num,
                s.spec.step_total,
                s.spec.icon.as_str(),
                s.spec.title.as_str(),
                s.spec.subtitle.as_str(),
            ),
            _ => (0u32, 0u32, "", "", ""),
        };

        let pct = if step_total > 0 {
            (step_num.saturating_sub(1) as f32 / step_total as f32) * 100.0
        } else {
            0.0
        };
        let bar = progress_bar(pct, 18);
        let pill = format!("{} / {}", step_num, step_total);

        let brand = if self.state.brand.is_empty() {
            "OPSINTELLIGENCE"
        } else {
            &self.state.brand
        };
        let top = Line::from(vec![
            Span::styled(
                brand.to_string(),
                Style::default()
                    .fg(theme::PRIMARY)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::raw("  "),
            Span::styled(bar, Style::default().fg(theme::PRIMARY)),
            Span::raw("  "),
            Span::styled(
                format!(" {} ", pill),
                Style::default()
                    .bg(theme::PRIMARY)
                    .fg(theme::BACKGROUND)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::raw("  "),
            Span::styled(
                format!("{}  {}", icon, title),
                Style::default()
                    .fg(theme::NEON)
                    .add_modifier(Modifier::BOLD),
            ),
        ]);
        f.render_widget(Paragraph::new(top), Rect { height: 1, ..area });

        if !subtitle.is_empty() {
            f.render_widget(
                Paragraph::new(Span::styled(
                    format!("    {}", subtitle),
                    theme::muted(),
                )),
                Rect {
                    y: area.y + 1,
                    height: 1,
                    ..area
                },
            );
        }
        f.render_widget(
            Paragraph::new(Span::styled(
                "╌".repeat(area.width as usize),
                Style::default().fg(theme::BORDER),
            )),
            Rect {
                y: area.y + 2,
                height: 1,
                ..area
            },
        );
    }
}

fn make_runtime(field: &WizardField) -> FieldRuntime {
    match field {
        WizardField::Input { default, placeholder, .. } => {
            let mut ta = TextArea::new(placeholder.clone());
            if !default.is_empty() {
                ta.set_value(default.clone());
            }
            FieldRuntime::Input(ta)
        }
        WizardField::Select { options, default, .. } => {
            let idx = options
                .iter()
                .position(|o| o.value == *default)
                .unwrap_or(0);
            FieldRuntime::Select { index: idx }
        }
        WizardField::MultiSelect { options, default, .. } => {
            let selected = options.iter().map(|o| default.contains(&o.value)).collect();
            FieldRuntime::MultiSelect {
                selected,
                cursor: 0,
            }
        }
        WizardField::Confirm { default, .. } => FieldRuntime::Confirm { value: *default },
        WizardField::Note { .. } => FieldRuntime::Note,
    }
}

fn first_interactive_field(fields: &[FieldRuntime]) -> Option<usize> {
    fields
        .iter()
        .position(|f| !matches!(f, FieldRuntime::Note))
}

fn handle_form_key(state: &mut FormState, key: KeyEvent) -> WizardOutbound {
    match key.code {
        KeyCode::Esc => {
            return WizardOutbound::Submit {
                id: state.spec.id,
                fields: json!({}),
                cancelled: true,
            };
        }
        KeyCode::Tab | KeyCode::Down => {
            advance_cursor(state, 1);
            return WizardOutbound::None;
        }
        KeyCode::BackTab | KeyCode::Up => {
            advance_cursor(state, -1);
            return WizardOutbound::None;
        }
        KeyCode::Enter => {
            // On the last interactive field, Enter submits. Otherwise advance.
            if is_last_interactive(state) {
                return submit_form(state);
            }
            advance_cursor(state, 1);
            return WizardOutbound::None;
        }
        KeyCode::Char('s') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            return submit_form(state);
        }
        _ => {}
    }

    let cursor = state.cursor;
    let spec = state.spec.fields.get(cursor).cloned();
    let rt = match state.fields.get_mut(cursor) {
        Some(rt) => rt,
        None => return WizardOutbound::None,
    };
    match (spec, rt) {
        (Some(WizardField::Select { options, .. }), FieldRuntime::Select { index }) => {
            match key.code {
                KeyCode::Left => {
                    if *index > 0 {
                        *index -= 1;
                    }
                }
                KeyCode::Right => {
                    if *index + 1 < options.len() {
                        *index += 1;
                    }
                }
                _ => {}
            }
        }
        (Some(WizardField::MultiSelect { options, .. }), FieldRuntime::MultiSelect { selected, cursor }) => {
            match key.code {
                KeyCode::Left => {
                    if *cursor > 0 {
                        *cursor -= 1;
                    }
                }
                KeyCode::Right => {
                    if *cursor + 1 < options.len() {
                        *cursor += 1;
                    }
                }
                KeyCode::Char(' ') => {
                    if let Some(v) = selected.get_mut(*cursor) {
                        *v = !*v;
                    }
                }
                _ => {}
            }
        }
        (Some(WizardField::Confirm { .. }), FieldRuntime::Confirm { value }) => {
            match key.code {
                KeyCode::Left | KeyCode::Right | KeyCode::Char(' ') => {
                    *value = !*value;
                }
                KeyCode::Char('y') | KeyCode::Char('Y') => *value = true,
                KeyCode::Char('n') | KeyCode::Char('N') => *value = false,
                _ => {}
            }
        }
        (Some(WizardField::Input { .. }), FieldRuntime::Input(ta)) => {
            let _ = ta.handle_key(key);
        }
        _ => {}
    }
    WizardOutbound::None
}

fn is_last_interactive(state: &FormState) -> bool {
    state
        .fields
        .iter()
        .enumerate()
        .skip(state.cursor + 1)
        .all(|(_, f)| matches!(f, FieldRuntime::Note))
}

fn advance_cursor(state: &mut FormState, delta: i32) {
    let n = state.fields.len();
    if n == 0 {
        return;
    }
    let mut i = state.cursor as i32;
    for _ in 0..n {
        i = (i + delta + n as i32) % n as i32;
        if !matches!(state.fields[i as usize], FieldRuntime::Note) {
            state.cursor = i as usize;
            return;
        }
    }
}

fn submit_form(state: &FormState) -> WizardOutbound {
    let mut map = serde_json::Map::new();
    for (spec, rt) in state.spec.fields.iter().zip(state.fields.iter()) {
        let (k, v) = match (spec, rt) {
            (WizardField::Input { key, .. }, FieldRuntime::Input(ta)) => {
                (key.clone(), Value::String(ta.value().to_string()))
            }
            (
                WizardField::Select { key, options, .. },
                FieldRuntime::Select { index },
            ) => {
                let val = options.get(*index).map(|o| o.value.clone()).unwrap_or_default();
                (key.clone(), Value::String(val))
            }
            (
                WizardField::MultiSelect { key, options, .. },
                FieldRuntime::MultiSelect { selected, .. },
            ) => {
                let vals: Vec<Value> = options
                    .iter()
                    .zip(selected.iter())
                    .filter_map(|(o, on)| if *on { Some(Value::String(o.value.clone())) } else { None })
                    .collect();
                (key.clone(), Value::Array(vals))
            }
            (WizardField::Confirm { key, .. }, FieldRuntime::Confirm { value }) => {
                (key.clone(), Value::Bool(*value))
            }
            _ => continue,
        };
        map.insert(k, v);
    }
    WizardOutbound::Submit {
        id: state.spec.id,
        fields: Value::Object(map),
        cancelled: false,
    }
}

fn render_form(f: &mut Frame, area: Rect, state: &FormState) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(theme::BORDER));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let mut lines: Vec<Line<'static>> = Vec::new();
    for (i, (spec, rt)) in state.spec.fields.iter().zip(state.fields.iter()).enumerate() {
        let focused = i == state.cursor;
        render_field(&mut lines, spec, rt, focused);
        lines.push(Line::raw(""));
    }
    f.render_widget(
        Paragraph::new(lines).wrap(Wrap { trim: false }),
        inner,
    );

    // Overlay the input text area on top of its placeholder line.
    if let Some(field) = state.spec.fields.get(state.cursor) {
        if let WizardField::Input { .. } = field {
            if let Some(FieldRuntime::Input(ta)) = state.fields.get(state.cursor) {
                // Approximate row of the input: 2 lines per field above + offset
                let y = inner.y + ((state.cursor as u16) * 3) + 1;
                if y < inner.y + inner.height {
                    let row = Rect {
                        x: inner.x + 4,
                        y,
                        width: inner.width.saturating_sub(4),
                        height: 1,
                    };
                    let view = TextAreaView {
                        area: ta,
                        style: Style::default().fg(theme::ON_SURFACE),
                        placeholder_style: theme::muted(),
                        focused: true,
                    };
                    f.render_widget(view, row);
                }
            }
        }
    }
}

fn render_field(
    out: &mut Vec<Line<'static>>,
    spec: &WizardField,
    rt: &FieldRuntime,
    focused: bool,
) {
    let prefix = if focused { "▸ " } else { "  " };
    let label_style = if focused {
        theme::primary()
    } else {
        theme::muted()
    };
    let label = match spec {
        WizardField::Input { label, .. }
        | WizardField::Select { label, .. }
        | WizardField::MultiSelect { label, .. }
        | WizardField::Confirm { label, .. } => label.clone(),
        WizardField::Note { title, .. } => title.clone(),
    };
    out.push(Line::from(vec![
        Span::styled(prefix.to_string(), label_style),
        Span::styled(label, label_style),
    ]));

    let desc = match spec {
        WizardField::Input { description, .. }
        | WizardField::Select { description, .. }
        | WizardField::MultiSelect { description, .. }
        | WizardField::Confirm { description, .. } => description.clone(),
        WizardField::Note { description, .. } => description.clone(),
    };
    if !desc.is_empty() {
        out.push(Line::from(vec![
            Span::raw("    "),
            Span::styled(desc, theme::muted()),
        ]));
    }

    match (spec, rt) {
        (WizardField::Input { password, .. }, FieldRuntime::Input(ta)) => {
            let display = if *password {
                "•".repeat(ta.value().chars().count())
            } else {
                ta.value().to_string()
            };
            out.push(Line::from(vec![
                Span::raw("    "),
                Span::styled(
                    if display.is_empty() { " ".to_string() } else { display },
                    Style::default().fg(theme::ON_SURFACE),
                ),
            ]));
        }
        (WizardField::Select { options, .. }, FieldRuntime::Select { index }) => {
            out.push(render_options_inline(options, *index, focused));
        }
        (
            WizardField::MultiSelect { options, .. },
            FieldRuntime::MultiSelect { selected, cursor },
        ) => {
            for (i, opt) in options.iter().enumerate() {
                let mark = if selected.get(i).copied().unwrap_or(false) { "[x]" } else { "[ ]" };
                let style = if focused && i == *cursor {
                    theme::primary()
                } else if selected.get(i).copied().unwrap_or(false) {
                    Style::default().fg(theme::ON_SURFACE)
                } else {
                    theme::muted()
                };
                out.push(Line::from(vec![
                    Span::styled(format!("    {} ", mark), style),
                    Span::styled(opt.label.clone(), style),
                ]));
            }
        }
        (WizardField::Confirm { affirmative, negative, .. }, FieldRuntime::Confirm { value }) => {
            let (a_style, n_style) = if *value {
                (theme::primary(), theme::muted())
            } else {
                (theme::muted(), theme::primary())
            };
            out.push(Line::from(vec![
                Span::raw("    "),
                Span::styled(format!(" {} ", affirmative), a_style),
                Span::raw("   "),
                Span::styled(format!(" {} ", negative), n_style),
            ]));
        }
        (WizardField::Note { .. }, FieldRuntime::Note) => {}
        _ => {}
    }
}

fn render_options_inline(options: &[WizardOption], index: usize, focused: bool) -> Line<'static> {
    let mut spans = vec![Span::raw("    ")];
    for (i, o) in options.iter().enumerate() {
        if i > 0 {
            spans.push(Span::styled("  ·  ", theme::muted()));
        }
        let active = i == index;
        let style = if active && focused {
            Style::default()
                .fg(theme::PRIMARY)
                .add_modifier(Modifier::BOLD | Modifier::UNDERLINED)
        } else if active {
            Style::default().fg(theme::PRIMARY)
        } else {
            theme::muted()
        };
        spans.push(Span::styled(o.label.clone(), style));
    }
    Line::from(spans)
}

fn render_side(f: &mut Frame, area: Rect, state: &mut SideState) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(theme::BORDER));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let mut lines: Vec<Line<'static>> = Vec::new();
    lines.push(Line::raw(""));
    lines.push(Line::from(vec![
        Span::styled(format!("  {}  ", state.spinner.frame()), theme::neon()),
        Span::styled(state.spec.label.clone(), theme::muted()),
    ]));
    if !state.spec.error.is_empty() {
        lines.push(Line::raw(""));
        lines.push(Line::from(vec![
            Span::styled("  ✗  ", theme::error_style()),
            Span::styled(state.spec.error.clone(), theme::error_style()),
        ]));
    }
    f.render_widget(
        Paragraph::new(lines).wrap(Wrap { trim: false }),
        inner,
    );
}

fn render_done(f: &mut Frame, area: Rect, message: &str) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(theme::SUCCESS));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let lines = vec![
        Line::raw(""),
        Line::from(vec![
            Span::styled("  ✓  ", Style::default().fg(theme::SUCCESS).add_modifier(Modifier::BOLD)),
            Span::styled("Setup complete", Style::default().fg(theme::ON_SURFACE).add_modifier(Modifier::BOLD)),
        ]),
        Line::raw(""),
        Line::from(Span::styled(format!("  {}", message), theme::muted())),
    ];
    f.render_widget(
        Paragraph::new(lines).wrap(Wrap { trim: false }),
        inner,
    );
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
