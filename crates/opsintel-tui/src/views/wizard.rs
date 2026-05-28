//! Wizard view — dashboard-style multi-pane form renderer.
//!
//! Visual language is modelled on Bagels / Posting (Textual apps):
//!   * single outer frame with the app title embedded in the top border
//!   * section labels inlined in each panel's top border
//!   * sharp single-line box-drawing everywhere (Borders::ALL, BorderType::Plain)
//!   * bullet markers before list items
//!   * status pill in the top-right of the outer frame
//!   * bottom command bar with `^k Action` style key hints
//!
//!   ┌─ OpsIntelligence · Onboard ─────  ████░░░░░░  03 / 14 ─┐
//!   │ ┌─ Steps ─────────┐ ┌─ ⚡  Smart Routing ──────────┐  │
//!   │ │ ● Provider   ✓  │ │ Auto-route prompts by         │  │
//!   │ │ ● Secondary  ✓  │ │ complexity to save 30–60% on  │  │
//!   │ │ ► Smart Routing │ │ LLM costs.                    │  │
//!   │ │   Routing       │ │                               │  │
//!   │ │   Embeddings    │ │ ┌─ Enable Smart Routing? ─┐  │  │
//!   │ │   …             │ │ │   Requires Docker.       │  │  │
//!   │ │                 │ │ │                          │  │  │
//!   │ │                 │ │ │   ┌─ Yes ─┐  ┌─ No ─┐    │  │  │
//!   │ │                 │ │ │   └───────┘  └──────┘    │  │  │
//!   │ │                 │ │ └──────────────────────────┘  │  │
//!   │ └─────────────────┘ │                  [ Submit ↵ ] │  │
//!   │                     └───────────────────────────────┘  │
//!   │  ^k field   ↑↓ pick   ⏎ submit   esc quit   click     │
//!   └──────────────────────────────────────────────────────────┘

use crate::protocol::{
    WizardField, WizardForm, WizardPlan, WizardSide, WizardState, WizardStep,
};
use crate::theme;
use crate::widgets::spinner::Spinner;
use crate::widgets::textarea::{TextArea, TextAreaView};
use crossterm::event::{
    KeyCode, KeyEvent, KeyEventKind, KeyModifiers, MouseButton, MouseEvent, MouseEventKind,
};
use ratatui::{
    layout::{Alignment, Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, BorderType, Borders, Paragraph, Wrap},
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
    fields: Vec<FieldRuntime>,
    cursor: usize,
    scroll: u16,
    hit_zones: Vec<HitZone>,
}

enum FieldRuntime {
    Input(TextArea),
    Select { index: usize },
    MultiSelect { selected: Vec<bool>, cursor: usize },
    Confirm { value: bool },
    Note,
}

struct HitZone {
    rect: Rect,
    action: HitAction,
}

#[derive(Clone, Copy)]
enum HitAction {
    FocusField(usize),
    SelectOption(usize, usize),
    ToggleMultiOption(usize, usize),
    SetConfirm(usize, bool),
    SubmitForm,
}

struct SideState {
    spec: WizardSide,
    spinner: Spinner,
}

pub struct WizardView {
    state: WizardState,
    plan: WizardPlan,
    active: ActiveStep,
    active_step_num: u32,
    active_step_total: u32,
}

impl WizardView {
    pub fn new(state: WizardState) -> Self {
        Self {
            state,
            plan: WizardPlan::default(),
            active: ActiveStep::None,
            active_step_num: 0,
            active_step_total: 0,
        }
    }

    pub fn apply_plan(&mut self, plan: WizardPlan) {
        self.plan = plan;
    }

    pub fn apply_step(&mut self, step: WizardStep) {
        match step {
            WizardStep::Form(spec) => {
                self.active_step_num = spec.step_num;
                self.active_step_total = spec.step_total;
                let fields: Vec<FieldRuntime> = spec.fields.iter().map(make_runtime).collect();
                let cursor = first_interactive_field(&fields).unwrap_or(0);
                self.active = ActiveStep::Form(FormState {
                    spec,
                    fields,
                    cursor,
                    scroll: 0,
                    hit_zones: Vec::new(),
                });
            }
            WizardStep::Side(spec) => {
                self.active_step_num = spec.step_num;
                self.active_step_total = spec.step_total;
                self.active = ActiveStep::Side(SideState {
                    spec,
                    spinner: Spinner::new(),
                });
            }
            WizardStep::Done { message } => {
                self.active = ActiveStep::Done(message);
            }
        }
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

    pub fn handle_mouse(&mut self, ev: MouseEvent) -> WizardOutbound {
        let ActiveStep::Form(state) = &mut self.active else {
            return WizardOutbound::None;
        };
        match ev.kind {
            MouseEventKind::Down(MouseButton::Left) => {
                let hit = state
                    .hit_zones
                    .iter()
                    .find(|hz| point_in(ev.column, ev.row, hz.rect))
                    .map(|hz| hz.action);
                match hit {
                    Some(HitAction::FocusField(idx)) => {
                        if !matches!(state.fields.get(idx), Some(FieldRuntime::Note)) {
                            state.cursor = idx;
                        }
                        WizardOutbound::None
                    }
                    Some(HitAction::SelectOption(field_idx, opt_idx)) => {
                        state.cursor = field_idx;
                        if let Some(FieldRuntime::Select { index }) =
                            state.fields.get_mut(field_idx)
                        {
                            *index = opt_idx;
                        }
                        WizardOutbound::None
                    }
                    Some(HitAction::ToggleMultiOption(field_idx, opt_idx)) => {
                        state.cursor = field_idx;
                        if let Some(FieldRuntime::MultiSelect { selected, cursor }) =
                            state.fields.get_mut(field_idx)
                        {
                            *cursor = opt_idx;
                            if let Some(v) = selected.get_mut(opt_idx) {
                                *v = !*v;
                            }
                        }
                        WizardOutbound::None
                    }
                    Some(HitAction::SetConfirm(field_idx, v)) => {
                        state.cursor = field_idx;
                        if let Some(FieldRuntime::Confirm { value }) =
                            state.fields.get_mut(field_idx)
                        {
                            *value = v;
                        }
                        WizardOutbound::None
                    }
                    Some(HitAction::SubmitForm) => submit_form(state),
                    None => WizardOutbound::None,
                }
            }
            MouseEventKind::ScrollUp => {
                state.scroll = state.scroll.saturating_sub(2);
                WizardOutbound::None
            }
            MouseEventKind::ScrollDown => {
                state.scroll = state.scroll.saturating_add(2);
                WizardOutbound::None
            }
            _ => WizardOutbound::None,
        }
    }

    pub fn render(&mut self, f: &mut Frame, area: Rect) {
        // ── Outer chrome ────────────────────────────────────────────────
        let outer_title = self.outer_title();
        let outer_block = Block::default()
            .borders(Borders::ALL)
            .border_type(BorderType::Plain)
            .border_style(Style::default().fg(theme::PRIMARY))
            .title(outer_title)
            .title_alignment(Alignment::Left);
        let inner = outer_block.inner(area);
        f.render_widget(outer_block, area);

        // Status pill (top-right inside the outer frame).
        self.render_pill(f, area);

        // ── Body + command bar ─────────────────────────────────────────
        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([Constraint::Min(5), Constraint::Length(1)])
            .split(inner);

        match &mut self.active {
            ActiveStep::None => render_placeholder(f, layout[0], "Waiting for first step…"),
            ActiveStep::Form(state) => {
                let body = split_sidebar(layout[0]);
                render_sidebar(
                    f,
                    body[0],
                    &self.plan,
                    self.active_step_num,
                );
                render_form(f, body[1], state);
            }
            ActiveStep::Side(state) => {
                let body = split_sidebar(layout[0]);
                render_sidebar(
                    f,
                    body[0],
                    &self.plan,
                    self.active_step_num,
                );
                render_side(f, body[1], state);
            }
            ActiveStep::Done(msg) => render_done(f, layout[0], msg),
        }

        render_command_bar(f, layout[1], &self.active);
    }

    fn outer_title(&self) -> Line<'static> {
        let brand = if self.state.brand.is_empty() {
            "OpsIntelligence"
        } else {
            &self.state.brand
        };
        let pct = if self.active_step_total > 0 {
            (self.active_step_num.saturating_sub(1) as f32 / self.active_step_total as f32) * 100.0
        } else {
            0.0
        };
        let bar = progress_bar(pct, 18);
        Line::from(vec![
            Span::raw("─ "),
            Span::styled(
                brand.to_string(),
                Style::default()
                    .fg(theme::PRIMARY)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::styled("  ·  ", theme::muted()),
            Span::styled(
                "Onboard",
                Style::default()
                    .fg(theme::ON_SURFACE)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::raw("   "),
            Span::styled(bar, Style::default().fg(theme::PRIMARY)),
            Span::raw(" "),
        ])
    }

    fn render_pill(&self, f: &mut Frame, outer: Rect) {
        let label = format!(
            " {:>2} / {:<2} ",
            self.active_step_num.max(1),
            self.active_step_total.max(1)
        );
        let w = label.chars().count() as u16;
        if outer.width <= w + 2 {
            return;
        }
        let r = Rect {
            x: outer.x + outer.width.saturating_sub(w + 2),
            y: outer.y,
            width: w,
            height: 1,
        };
        let style = Style::default()
            .bg(theme::PRIMARY)
            .fg(theme::BACKGROUND)
            .add_modifier(Modifier::BOLD);
        f.render_widget(Paragraph::new(Span::styled(label, style)), r);
    }
}

// ── Layout helpers ──────────────────────────────────────────────────────

fn split_sidebar(area: Rect) -> std::rc::Rc<[Rect]> {
    Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Length(24), Constraint::Min(20)])
        .split(area)
}

// ── Sidebar ─────────────────────────────────────────────────────────────

fn render_sidebar(f: &mut Frame, area: Rect, plan: &WizardPlan, active_num: u32) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(theme::OUTLINE_VARIANT))
        .title(Line::from(vec![
            Span::styled("─ ", theme::muted()),
            Span::styled("Steps", theme::muted()),
            Span::styled(" ", theme::muted()),
        ]));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let mut lines: Vec<Line<'static>> = Vec::new();
    if plan.steps.is_empty() {
        lines.push(Line::from(Span::styled(
            format!("Step {}", active_num),
            theme::muted(),
        )));
    } else {
        // Determine which group is active: the last group whose `step_num`
        // is <= active_num. Groups before it are completed; groups after are
        // pending. Falls back to positional 1-based index when step_num is 0
        // (older Go binaries that don't emit the field).
        let active_idx: Option<usize> = {
            let mut found: Option<usize> = None;
            for (i, item) in plan.steps.iter().enumerate() {
                let start = if item.step_num == 0 { (i + 1) as u32 } else { item.step_num };
                if start <= active_num {
                    found = Some(i);
                } else {
                    break;
                }
            }
            found
        };
        for (i, item) in plan.steps.iter().enumerate() {
            let (marker, name_style) = if active_idx.is_some_and(|a| i < a) {
                (
                    Span::styled(
                        "● ",
                        Style::default().fg(theme::SUCCESS),
                    ),
                    Style::default()
                        .fg(theme::ON_SURFACE)
                        .add_modifier(Modifier::DIM),
                )
            } else if active_idx == Some(i) {
                (
                    Span::styled(
                        "► ",
                        Style::default()
                            .fg(theme::PRIMARY)
                            .add_modifier(Modifier::BOLD),
                    ),
                    Style::default()
                        .fg(theme::PRIMARY)
                        .add_modifier(Modifier::BOLD),
                )
            } else {
                (Span::styled("● ", theme::muted()), theme::muted())
            };
            let title = if item.title.is_empty() {
                format!("Step {}", i + 1)
            } else {
                item.title.clone()
            };
            lines.push(Line::from(vec![
                Span::raw(" "),
                marker,
                Span::styled(truncate(&title, 18), name_style),
            ]));
        }
    }
    f.render_widget(
        Paragraph::new(lines).wrap(Wrap { trim: false }),
        inner,
    );
}

// ── Form panel ──────────────────────────────────────────────────────────

fn render_form(f: &mut Frame, area: Rect, state: &mut FormState) {
    let title_line = Line::from(vec![
        Span::styled("─ ", Style::default().fg(theme::PRIMARY)),
        Span::styled(state.spec.icon.clone(), Style::default().fg(theme::PRIMARY)),
        Span::raw("  "),
        Span::styled(
            state.spec.title.clone(),
            Style::default()
                .fg(theme::PRIMARY)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw(" "),
    ]);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(theme::PRIMARY))
        .title(title_line);
    let inner = block.inner(area);
    f.render_widget(block, area);

    state.hit_zones.clear();

    // Subtitle row (1 line).
    let subtitle_h: u16 = if state.spec.subtitle.is_empty() { 0 } else { 2 };
    if subtitle_h > 0 {
        let r = Rect {
            x: inner.x + 1,
            y: inner.y,
            width: inner.width.saturating_sub(2),
            height: 1,
        };
        f.render_widget(
            Paragraph::new(Span::styled(state.spec.subtitle.clone(), theme::muted()))
                .wrap(Wrap { trim: false }),
            r,
        );
    }

    // Field area + Submit row reserve.
    let fields_area = Rect {
        x: inner.x + 1,
        y: inner.y + subtitle_h,
        width: inner.width.saturating_sub(2),
        height: inner.height.saturating_sub(subtitle_h + 2),
    };

    let mut y = fields_area.y.saturating_sub(state.scroll);
    let cursor = state.cursor;
    let spec_fields: Vec<WizardField> = state.spec.fields.clone();
    let mut new_hits: Vec<HitZone> = Vec::new();
    for (i, (spec, rt)) in spec_fields.iter().zip(state.fields.iter()).enumerate() {
        let focused = i == cursor;
        let h = field_height(spec);
        if y >= fields_area.y && y + h <= fields_area.y + fields_area.height {
            let card_rect = Rect {
                x: fields_area.x,
                y,
                width: fields_area.width,
                height: h,
            };
            render_field_card(f, card_rect, i, spec, rt, focused, &mut new_hits);
        }
        y = y.saturating_add(h);
    }
    state.hit_zones = new_hits;

    // Submit "button" — full-width bottom row, primary background.
    let btn_label = " Submit ↵ ";
    let btn_w = btn_label.chars().count() as u16 + 4;
    let btn_rect = Rect {
        x: inner.x + inner.width.saturating_sub(btn_w + 1),
        y: inner.y + inner.height.saturating_sub(1),
        width: btn_w,
        height: 1,
    };
    let btn_style = Style::default()
        .bg(theme::PRIMARY)
        .fg(theme::BACKGROUND)
        .add_modifier(Modifier::BOLD);
    f.render_widget(
        Paragraph::new(Span::styled(format!("  {}  ", btn_label), btn_style))
            .alignment(Alignment::Center),
        btn_rect,
    );
    state.hit_zones.push(HitZone {
        rect: btn_rect,
        action: HitAction::SubmitForm,
    });
}

// ── Field cards ─────────────────────────────────────────────────────────

fn render_field_card(
    f: &mut Frame,
    area: Rect,
    field_idx: usize,
    spec: &WizardField,
    rt: &FieldRuntime,
    focused: bool,
    hits: &mut Vec<HitZone>,
) {
    let border_style = if focused {
        Style::default().fg(theme::PRIMARY)
    } else {
        Style::default().fg(theme::OUTLINE_VARIANT)
    };
    let label = field_label(spec);
    let prefix = if focused { "▸ " } else { "  " };
    let title_style = if focused {
        Style::default()
            .fg(theme::PRIMARY)
            .add_modifier(Modifier::BOLD)
    } else {
        theme::muted()
    };
    let title_line = Line::from(vec![
        Span::styled("─ ", border_style),
        Span::styled(prefix, title_style),
        Span::styled(label, title_style),
        Span::raw(" "),
    ]);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(border_style)
        .title(title_line);
    let inner = block.inner(area);
    f.render_widget(block, area);

    hits.push(HitZone {
        rect: area,
        action: HitAction::FocusField(field_idx),
    });

    let desc = field_description(spec);
    let body_top = if desc.is_empty() {
        inner.y
    } else {
        let desc_rect = Rect {
            x: inner.x + 1,
            y: inner.y,
            width: inner.width.saturating_sub(2),
            height: 1,
        };
        f.render_widget(
            Paragraph::new(Span::styled(desc, theme::muted())),
            desc_rect,
        );
        inner.y + 1
    };
    let body_rect = Rect {
        x: inner.x + 2,
        y: body_top,
        width: inner.width.saturating_sub(4),
        height: inner.height.saturating_sub(body_top - inner.y),
    };

    match (spec, rt) {
        (WizardField::Input { password, .. }, FieldRuntime::Input(ta)) => {
            let raw = ta.value();
            let display: String = if *password {
                "•".repeat(raw.chars().count())
            } else {
                raw.to_string()
            };
            let row = Rect { height: 1, ..body_rect };
            if focused {
                f.render_widget(
                    Paragraph::new(Span::styled("› ", theme::primary())),
                    Rect { width: 2, ..row },
                );
                let view = TextAreaView {
                    area: ta,
                    style: Style::default().fg(theme::ON_SURFACE),
                    placeholder_style: theme::muted(),
                    focused: true,
                };
                f.render_widget(view, Rect {
                    x: row.x + 2,
                    width: row.width.saturating_sub(2),
                    ..row
                });
            } else {
                f.render_widget(
                    Paragraph::new(Line::from(vec![
                        Span::styled("› ", theme::muted()),
                        Span::styled(
                            if display.is_empty() { "—".to_string() } else { display },
                            Style::default().fg(theme::ON_SURFACE),
                        ),
                    ])),
                    row,
                );
            }
        }
        (WizardField::Select { options, .. }, FieldRuntime::Select { index }) => {
            for (i, opt) in options.iter().enumerate() {
                let row_rect = Rect {
                    x: body_rect.x,
                    y: body_rect.y + (i as u16),
                    width: body_rect.width,
                    height: 1,
                };
                if row_rect.y >= body_rect.y + body_rect.height {
                    break;
                }
                let selected = i == *index;
                let dot_style = if selected {
                    Style::default()
                        .fg(theme::PRIMARY)
                        .add_modifier(Modifier::BOLD)
                } else {
                    theme::muted()
                };
                let label_style = if selected {
                    Style::default()
                        .fg(theme::ON_SURFACE)
                        .add_modifier(Modifier::BOLD)
                } else {
                    theme::muted()
                };
                let line = Line::from(vec![
                    Span::styled("● ", dot_style),
                    Span::styled(opt.label.clone(), label_style),
                ]);
                f.render_widget(Paragraph::new(line), row_rect);
                hits.push(HitZone {
                    rect: row_rect,
                    action: HitAction::SelectOption(field_idx, i),
                });
            }
        }
        (WizardField::MultiSelect { options, .. }, FieldRuntime::MultiSelect { selected, cursor }) => {
            for (i, opt) in options.iter().enumerate() {
                let row_rect = Rect {
                    x: body_rect.x,
                    y: body_rect.y + (i as u16),
                    width: body_rect.width,
                    height: 1,
                };
                if row_rect.y >= body_rect.y + body_rect.height {
                    break;
                }
                let on = selected.get(i).copied().unwrap_or(false);
                let mark = if on { "[x]" } else { "[ ]" };
                let mark_style = if focused && i == *cursor {
                    Style::default()
                        .fg(theme::PRIMARY)
                        .add_modifier(Modifier::BOLD)
                } else if on {
                    Style::default().fg(theme::SUCCESS)
                } else {
                    theme::muted()
                };
                let label_style = if on {
                    Style::default().fg(theme::ON_SURFACE)
                } else {
                    theme::muted()
                };
                let line = Line::from(vec![
                    Span::styled(format!("{} ", mark), mark_style),
                    Span::styled(opt.label.clone(), label_style),
                ]);
                f.render_widget(Paragraph::new(line), row_rect);
                hits.push(HitZone {
                    rect: row_rect,
                    action: HitAction::ToggleMultiOption(field_idx, i),
                });
            }
        }
        (WizardField::Confirm { affirmative, negative, .. }, FieldRuntime::Confirm { value }) => {
            let aff = format!(" {} ", affirmative);
            let neg = format!(" {} ", negative);
            let aw = aff.chars().count() as u16;
            let nw = neg.chars().count() as u16;
            let aff_rect = Rect {
                x: body_rect.x,
                y: body_rect.y,
                width: aw,
                height: 1,
            };
            let neg_rect = Rect {
                x: aff_rect.x + aw + 2,
                y: body_rect.y,
                width: nw,
                height: 1,
            };
            let active_style = Style::default()
                .bg(theme::PRIMARY)
                .fg(theme::BACKGROUND)
                .add_modifier(Modifier::BOLD);
            let inactive_style = Style::default()
                .fg(theme::MUTED)
                .add_modifier(Modifier::DIM);
            f.render_widget(
                Paragraph::new(Span::styled(
                    aff,
                    if *value { active_style } else { inactive_style },
                )),
                aff_rect,
            );
            f.render_widget(
                Paragraph::new(Span::styled(
                    neg,
                    if !*value { active_style } else { inactive_style },
                )),
                neg_rect,
            );
            hits.push(HitZone {
                rect: aff_rect,
                action: HitAction::SetConfirm(field_idx, true),
            });
            hits.push(HitZone {
                rect: neg_rect,
                action: HitAction::SetConfirm(field_idx, false),
            });
        }
        (WizardField::Note { .. }, FieldRuntime::Note) => {}
        _ => {}
    }
}

fn field_height(spec: &WizardField) -> u16 {
    let desc_rows = if field_description(spec).is_empty() { 0 } else { 1 };
    let body_rows = match spec {
        WizardField::Input { .. } => 1,
        WizardField::Select { options, .. } => (options.len().min(8)) as u16,
        WizardField::MultiSelect { options, .. } => (options.len().min(8)) as u16,
        WizardField::Confirm { .. } => 1,
        WizardField::Note { .. } => 0,
    };
    2 + desc_rows + body_rows.max(1)
}

// ── Side-effect step ─────────────────────────────────────────────────────

fn render_side(f: &mut Frame, area: Rect, state: &mut SideState) {
    let title_line = Line::from(vec![
        Span::styled("─ ", Style::default().fg(theme::PRIMARY)),
        Span::styled(state.spec.icon.clone(), Style::default().fg(theme::PRIMARY)),
        Span::raw("  "),
        Span::styled(
            state.spec.title.clone(),
            Style::default()
                .fg(theme::PRIMARY)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw(" "),
    ]);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(theme::PRIMARY))
        .title(title_line);
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

fn render_placeholder(f: &mut Frame, area: Rect, text: &str) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(theme::OUTLINE_VARIANT));
    let inner = block.inner(area);
    f.render_widget(block, area);
    f.render_widget(
        Paragraph::new(Span::styled(text.to_string(), theme::muted())),
        inner,
    );
}

fn render_done(f: &mut Frame, area: Rect, message: &str) {
    let title_line = Line::from(vec![
        Span::styled("─ ", Style::default().fg(theme::SUCCESS)),
        Span::styled(
            "Done",
            Style::default()
                .fg(theme::SUCCESS)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw(" "),
    ]);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(theme::SUCCESS))
        .title(title_line);
    let inner = block.inner(area);
    f.render_widget(block, area);
    let lines = vec![
        Line::raw(""),
        Line::from(vec![
            Span::styled(
                "  ✓  ",
                Style::default()
                    .fg(theme::SUCCESS)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::styled(
                "Setup complete",
                Style::default()
                    .fg(theme::ON_SURFACE)
                    .add_modifier(Modifier::BOLD),
            ),
        ]),
        Line::raw(""),
        Line::from(Span::styled(format!("  {}", message), theme::muted())),
    ];
    f.render_widget(
        Paragraph::new(lines).wrap(Wrap { trim: false }),
        inner,
    );
}

// ── Bottom command bar ──────────────────────────────────────────────────

fn render_command_bar(f: &mut Frame, area: Rect, active: &ActiveStep) {
    let entries: &[(&str, &str)] = match active {
        ActiveStep::Form(_) => &[
            ("⇥", "Field"),
            ("↑↓", "Pick"),
            ("⏎", "Submit"),
            ("⎋", "Quit"),
            ("⌖", "Click"),
        ],
        ActiveStep::Side(_) => &[("⌃C", "Abort")],
        ActiveStep::Done(_) => &[("⏎", "Exit")],
        ActiveStep::None => &[],
    };

    let mut spans: Vec<Span> = vec![Span::raw(" ")];
    for (i, (key, label)) in entries.iter().enumerate() {
        if i > 0 {
            spans.push(Span::styled("   ", theme::muted()));
        }
        spans.push(Span::styled(
            format!(" {} ", key),
            Style::default()
                .bg(theme::PRIMARY)
                .fg(theme::BACKGROUND)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" "));
        spans.push(Span::styled(label.to_string(), theme::muted()));
    }
    f.render_widget(Paragraph::new(Line::from(spans)), area);
}

// ── Key handling ─────────────────────────────────────────────────────────

fn handle_form_key(state: &mut FormState, key: KeyEvent) -> WizardOutbound {
    match key.code {
        KeyCode::Esc => {
            return WizardOutbound::Submit {
                id: state.spec.id,
                fields: json!({}),
                cancelled: true,
            };
        }
        KeyCode::Tab => {
            advance_cursor(state, 1);
            return WizardOutbound::None;
        }
        KeyCode::BackTab => {
            advance_cursor(state, -1);
            return WizardOutbound::None;
        }
        KeyCode::Char('s') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            return submit_form(state);
        }
        KeyCode::Enter => {
            // Enter always submits the form. Use Tab / ↓ / j to advance
            // between fields. This matches huh's behavior and avoids the
            // surprise where pressing Enter on a non-final Select silently
            // advances cursor without any visible change.
            return submit_form(state);
        }
        KeyCode::PageUp => {
            state.scroll = state.scroll.saturating_sub(4);
            return WizardOutbound::None;
        }
        KeyCode::PageDown => {
            state.scroll = state.scroll.saturating_add(4);
            return WizardOutbound::None;
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
                KeyCode::Up | KeyCode::Char('k') => {
                    if *index > 0 {
                        *index -= 1;
                    }
                }
                KeyCode::Down | KeyCode::Char('j') => {
                    if *index + 1 < options.len() {
                        *index += 1;
                    }
                }
                _ => {}
            }
        }
        (
            Some(WizardField::MultiSelect { options, .. }),
            FieldRuntime::MultiSelect { selected, cursor },
        ) => match key.code {
            KeyCode::Up | KeyCode::Char('k') => {
                if *cursor > 0 {
                    *cursor -= 1;
                }
            }
            KeyCode::Down | KeyCode::Char('j') => {
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
        },
        (Some(WizardField::Confirm { .. }), FieldRuntime::Confirm { value }) => match key.code {
            KeyCode::Left | KeyCode::Right | KeyCode::Char(' ') => *value = !*value,
            KeyCode::Char('y') | KeyCode::Char('Y') => *value = true,
            KeyCode::Char('n') | KeyCode::Char('N') => *value = false,
            _ => {}
        },
        (Some(WizardField::Input { .. }), FieldRuntime::Input(ta)) => {
            let _ = ta.handle_key(key);
        }
        _ => {}
    }
    WizardOutbound::None
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

#[allow(dead_code)]
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
            let idx = options.iter().position(|o| o.value == *default).unwrap_or(0);
            FieldRuntime::Select { index: idx }
        }
        WizardField::MultiSelect { options, default, .. } => {
            let selected = options.iter().map(|o| default.contains(&o.value)).collect();
            FieldRuntime::MultiSelect { selected, cursor: 0 }
        }
        WizardField::Confirm { default, .. } => FieldRuntime::Confirm { value: *default },
        WizardField::Note { .. } => FieldRuntime::Note,
    }
}

fn first_interactive_field(fields: &[FieldRuntime]) -> Option<usize> {
    fields.iter().position(|f| !matches!(f, FieldRuntime::Note))
}

fn field_label(spec: &WizardField) -> String {
    match spec {
        WizardField::Input { label, .. }
        | WizardField::Select { label, .. }
        | WizardField::MultiSelect { label, .. }
        | WizardField::Confirm { label, .. } => label.clone(),
        WizardField::Note { title, .. } => title.clone(),
    }
}

fn field_description(spec: &WizardField) -> String {
    match spec {
        WizardField::Input { description, .. }
        | WizardField::Select { description, .. }
        | WizardField::MultiSelect { description, .. }
        | WizardField::Confirm { description, .. } => description.clone(),
        WizardField::Note { description, .. } => description.clone(),
    }
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

fn point_in(x: u16, y: u16, r: Rect) -> bool {
    x >= r.x && x < r.x + r.width && y >= r.y && y < r.y + r.height
}
