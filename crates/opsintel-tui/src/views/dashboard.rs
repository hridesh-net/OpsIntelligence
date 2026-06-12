//! Dashboard view — port of `cmd/opsintelligence/tui/dashboard.go`.
//!
//! Receives a `DashboardSnapshot` periodically from the Go side; this view is
//! responsible only for tab navigation, search filtering, and styling.

use crate::protocol::{
    AgentInfo, DashboardEditResult, DashboardSnapshot, DashboardState, DashboardStatus, KeyValue,
    LogEntry,
};
use crate::theme;
use crate::widgets::textarea::{TextArea, TextAreaView};
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, BorderType, Borders, Paragraph, Wrap},
    Frame,
};
use std::time::{Duration, Instant};

const TABS: &[&str] = &["Status", "Config", "Limits", "Usage", "Agents", "Logs"];

pub enum DashboardOutbound {
    None,
    /// Esc/q in overlay mode: just dismiss this view, host stays.
    Dismiss,
    /// Esc/q in standalone mode: quit the program.
    Quit,
    /// User submitted an inline edit. Bridge forwards as `dashboard.edit`
    /// `{yaml_path, value}` to Go which patches opsintelligence.yaml.
    Edit { yaml_path: String, value: String },
}

/// Inline editor state for `e` on a Config/Limits row. Hidden by default.
struct EditState {
    yaml_path: String,
    label: String,
    hint: String,
    /// When non-empty, present as a vertical select instead of free-form text.
    choices: Vec<String>,
    select_index: usize,
    input: TextArea,
}

/// Transient bottom-row toast displayed after dashboard.edit_result comes back.
struct Toast {
    text: String,
    color: ratatui::style::Color,
    expires: Instant,
}

pub struct DashboardView {
    pub state: DashboardState,
    pub snap: DashboardSnapshot,
    active_tab: usize,
    search: TextArea,
    search_active: bool,
    scroll: u16,
    edit: Option<EditState>,
    toast: Option<Toast>,
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
            edit: None,
            toast: None,
        }
    }

    pub fn apply_snapshot(&mut self, snap: DashboardSnapshot) {
        self.snap = snap;
    }

    pub fn apply_edit_result(&mut self, res: DashboardEditResult) {
        let (text, color) = if res.ok {
            let msg = if res.message.is_empty() {
                format!("saved {}", res.yaml_path)
            } else {
                res.message
            };
            (msg, theme::SUCCESS)
        } else {
            let msg = if res.error.is_empty() {
                format!("edit failed for {}", res.yaml_path)
            } else {
                format!("{}: {}", res.yaml_path, res.error)
            };
            (msg, theme::ERROR_COLOR)
        };
        self.toast = Some(Toast {
            text,
            color,
            expires: Instant::now() + Duration::from_secs(5),
        });
    }

    /// Currently-active row collection for the visible tab. Used by `e` to
    /// look up the row under the scroll cursor.
    fn current_rows(&self) -> &[KeyValue] {
        match self.active_tab {
            1 => &self.snap.config,
            2 => &self.snap.limits,
            3 => &self.snap.usage,
            _ => &[],
        }
    }

    /// Return the KeyValue currently under the user's cursor on Config/Limits.
    /// We don't have per-row selection (the body is a scrolling Paragraph), so
    /// "cursor" means: the first editable row whose index >= self.scroll.
    /// Pressing `e` repeatedly therefore walks through editable rows top-down.
    fn first_editable_under_cursor(&self) -> Option<&KeyValue> {
        let rows = self.current_rows();
        // Rows have a header + blank line; account for those when matching
        // against self.scroll. Using simple heuristic: start from scroll.
        let start = self.scroll as usize;
        rows.iter()
            .enumerate()
            .find(|(i, r)| *i >= start && !r.yaml_path.is_empty())
            .or_else(|| rows.iter().enumerate().find(|(_, r)| !r.yaml_path.is_empty()))
            .map(|(_, r)| r)
    }

    fn open_editor_for(&mut self, kv_row: KeyValue) {
        let mut ta = TextArea::new("new value…");
        ta.set_value(kv_row.v.clone());
        let select_index = kv_row
            .choices
            .iter()
            .position(|c| c == &kv_row.v)
            .unwrap_or(0);
        self.edit = Some(EditState {
            yaml_path: kv_row.yaml_path,
            label: kv_row.k,
            hint: kv_row.hint,
            choices: kv_row.choices,
            select_index,
            input: ta,
        });
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> DashboardOutbound {
        if key.kind != KeyEventKind::Press {
            return DashboardOutbound::None;
        }
        if let Some(e) = self.edit.as_mut() {
            match key.code {
                KeyCode::Esc => {
                    self.edit = None;
                    return DashboardOutbound::None;
                }
                KeyCode::Enter => {
                    let (yaml_path, value) = if !e.choices.is_empty() {
                        (
                            e.yaml_path.clone(),
                            e.choices
                                .get(e.select_index)
                                .cloned()
                                .unwrap_or_default(),
                        )
                    } else {
                        (e.yaml_path.clone(), e.input.value().to_string())
                    };
                    self.edit = None;
                    return DashboardOutbound::Edit { yaml_path, value };
                }
                KeyCode::Up | KeyCode::Char('k') if !e.choices.is_empty() => {
                    if e.select_index > 0 {
                        e.select_index -= 1;
                    }
                    return DashboardOutbound::None;
                }
                KeyCode::Down | KeyCode::Char('j') if !e.choices.is_empty() => {
                    if e.select_index + 1 < e.choices.len() {
                        e.select_index += 1;
                    }
                    return DashboardOutbound::None;
                }
                _ if e.choices.is_empty() => {
                    let _ = e.input.handle_key(key);
                    return DashboardOutbound::None;
                }
                _ => return DashboardOutbound::None,
            }
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
            KeyCode::Char('e') | KeyCode::Char('E') => {
                // Edit the first editable row on the current tab; ignored on
                // tabs with no editable rows (Status, Usage, Agents, Logs).
                if matches!(self.active_tab, 1 | 2) {
                    if let Some(row) = self.first_editable_under_cursor().cloned() {
                        self.open_editor_for(row);
                    } else {
                        self.toast = Some(Toast {
                            text: "no editable values on this tab".into(),
                            color: theme::OUTLINE_VARIANT,
                            expires: Instant::now() + Duration::from_secs(2),
                        });
                    }
                }
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
        // Outer frame.
        let section = if self.state.context_label.is_empty() {
            "Status".to_string()
        } else {
            self.state.context_label.clone()
        };
        let inner = crate::widgets::chrome::outer_block(
            f,
            area,
            "OpsIntelligence",
            &section,
            None,
        );
        // Status pill: alive indicator if we have one.
        let pill = if self.snap.status.alive {
            " RUNNING "
        } else if self.snap.status.pid > 0 {
            " STOPPED "
        } else {
            "         "
        };
        let pill_bg = if self.snap.status.alive {
            theme::SUCCESS
        } else if self.snap.status.pid > 0 {
            theme::ERROR_COLOR
        } else {
            theme::OUTLINE_VARIANT
        };
        crate::widgets::chrome::render_pill(f, area, pill, pill_bg);

        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1), // tab row
                Constraint::Length(3), // search bar
                Constraint::Min(3),    // body panel
                Constraint::Length(1), // command bar
            ])
            .split(inner);

        // Tab row.
        let mut tab_spans: Vec<Span> = vec![Span::raw(" ")];
        for (i, t) in TABS.iter().enumerate() {
            if i > 0 {
                tab_spans.push(Span::styled(" │ ", theme::muted()));
            }
            if i == self.active_tab {
                tab_spans.push(Span::styled(
                    format!(" {} ", t),
                    Style::default()
                        .bg(theme::PRIMARY)
                        .fg(theme::BACKGROUND)
                        .add_modifier(Modifier::BOLD),
                ));
            } else {
                tab_spans.push(Span::styled(format!(" {} ", t), theme::muted()));
            }
        }
        f.render_widget(Paragraph::new(Line::from(tab_spans)), layout[0]);

        // Search bar (panel-style).
        let search_block = crate::widgets::chrome::panel_block(
            "Search",
            if self.search_active { theme::PRIMARY } else { theme::OUTLINE_VARIANT },
            self.search_active,
        );
        let search_inner = search_block.inner(layout[1]);
        f.render_widget(search_block, layout[1]);
        f.render_widget(
            TextAreaView {
                area: &self.search,
                style: Style::default().fg(theme::ON_SURFACE),
                placeholder_style: theme::muted(),
                focused: self.search_active,
            },
            Rect {
                x: search_inner.x + 1,
                width: search_inner.width.saturating_sub(2),
                ..search_inner
            },
        );

        // Body panel (titled with active tab name).
        let body_title = TABS[self.active_tab.min(TABS.len() - 1)];
        let body_block = crate::widgets::chrome::panel_block(body_title, theme::PRIMARY, true);
        let body_inner = body_block.inner(layout[2]);
        f.render_widget(body_block, layout[2]);

        let query = self.search.value().to_ascii_lowercase();
        let q = query.trim();
        let lines = match self.active_tab {
            0 => render_status(&self.snap.status, q),
            1 => render_kv(&self.snap.config, "Configuration", q),
            2 => render_kv(&self.snap.limits, "Limits", q),
            3 => render_usage(&self.snap.usage, &self.snap.usage_empty_hint, q),
            4 => render_agents(&self.snap.agents, &self.snap.agents_hint, q),
            5 => render_logs(&self.snap.logs, &self.snap.log_source_path, q),
            _ => Vec::new(),
        };
        let max_scroll = (lines.len() as u16).saturating_sub(body_inner.height);
        if self.scroll > max_scroll {
            self.scroll = max_scroll;
        }
        f.render_widget(
            Paragraph::new(lines)
                .wrap(Wrap { trim: false })
                .scroll((self.scroll, 0)),
            body_inner,
        );

        // Command bar.
        let editable_tab = matches!(self.active_tab, 1 | 2);
        let entries: &[(&str, &str)] = match (self.state.overlay, editable_tab) {
            (true, true) => &[
                ("⇥", "Tab"),
                ("/", "Search"),
                ("E", "Edit"),
                ("⎋", "Close"),
            ],
            (true, false) => &[
                ("⇥", "Tab"),
                ("/", "Search"),
                ("⎋", "Close"),
            ],
            (false, true) => &[
                ("⇥", "Tab"),
                ("/", "Search"),
                ("E", "Edit"),
                ("Q", "Quit"),
            ],
            (false, false) => &[
                ("⇥", "Tab"),
                ("/", "Search"),
                ("Q", "Quit"),
            ],
        };
        crate::widgets::chrome::render_command_bar(f, layout[3], entries);

        // Toast at the bottom-most row, overlayed on top of body. Auto-expires.
        if let Some(t) = &self.toast {
            if Instant::now() < t.expires {
                let rect = Rect {
                    x: area.x + 2,
                    y: area.y + area.height.saturating_sub(2),
                    width: area.width.saturating_sub(4),
                    height: 1,
                };
                let style = Style::default()
                    .bg(t.color)
                    .fg(theme::BACKGROUND)
                    .add_modifier(Modifier::BOLD);
                let pad = format!(" {} ", t.text);
                f.render_widget(
                    Paragraph::new(Span::styled(pad, style)),
                    rect,
                );
            } else {
                self.toast = None;
            }
        }

        // Inline editor overlay (drawn on top of everything else).
        if let Some(e) = &mut self.edit {
            render_editor(f, area, e);
        }
    }
}

fn render_editor(f: &mut Frame, area: Rect, e: &mut EditState) {
    // Centered modal: 60% width, 8 rows minimum.
    let w = (area.width as u32 * 6 / 10) as u16;
    let w = w.max(40).min(area.width.saturating_sub(4));
    let h_choices = (e.choices.len() as u16).min(6) + 5;
    let h = if e.choices.is_empty() { 7 } else { h_choices };
    let h = h.min(area.height.saturating_sub(4));
    let rect = Rect {
        x: area.x + (area.width.saturating_sub(w)) / 2,
        y: area.y + (area.height.saturating_sub(h)) / 2,
        width: w,
        height: h,
    };
    // Repaint area with background to fully mask body content underneath.
    f.render_widget(
        Block::default().style(Style::default().bg(theme::BACKGROUND)),
        rect,
    );

    let title = Line::from(vec![
        Span::styled("─ ", Style::default().fg(theme::PRIMARY)),
        Span::styled(
            "Edit",
            Style::default().fg(theme::PRIMARY).add_modifier(Modifier::BOLD),
        ),
        Span::raw("  "),
        Span::styled(e.label.clone(), theme::muted()),
        Span::raw(" "),
    ]);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(theme::PRIMARY))
        .style(Style::default().bg(theme::BACKGROUND))
        .title(title);
    let inner = block.inner(rect);
    f.render_widget(block, rect);

    let path_line = Line::from(vec![
        Span::raw(" "),
        Span::styled("yaml: ", theme::muted()),
        Span::styled(
            e.yaml_path.clone(),
            Style::default().fg(theme::ON_SURFACE).add_modifier(Modifier::BOLD),
        ),
    ]);
    let hint_line = if e.hint.is_empty() {
        Line::raw("")
    } else {
        Line::from(vec![
            Span::raw(" "),
            Span::styled(e.hint.clone(), theme::muted()),
        ])
    };

    if e.choices.is_empty() {
        // Free-form text editor.
        let chunks = [
            Rect { x: inner.x, y: inner.y, width: inner.width, height: 1 },
            Rect { x: inner.x, y: inner.y + 1, width: inner.width, height: 1 },
            Rect { x: inner.x, y: inner.y + 3, width: inner.width, height: 1 },
            Rect {
                x: inner.x,
                y: inner.y + inner.height.saturating_sub(1),
                width: inner.width,
                height: 1,
            },
        ];
        f.render_widget(Paragraph::new(path_line), chunks[0]);
        f.render_widget(Paragraph::new(hint_line), chunks[1]);
        // Input row.
        f.render_widget(
            Paragraph::new(Line::from(vec![
                Span::raw(" "),
                Span::styled("› ", theme::primary()),
            ])),
            Rect { width: 4, ..chunks[2] },
        );
        let view = TextAreaView {
            area: &e.input,
            style: Style::default().fg(theme::ON_SURFACE),
            placeholder_style: theme::muted(),
            focused: true,
        };
        let input_rect = Rect {
            x: chunks[2].x + 4,
            y: chunks[2].y,
            width: chunks[2].width.saturating_sub(4),
            height: 1,
        };
        f.render_widget(view, input_rect);
        f.render_widget(
            Paragraph::new(Line::from(vec![
                Span::raw(" "),
                Span::styled("⏎ ", theme::primary()),
                Span::styled("Save", theme::muted()),
                Span::raw("    "),
                Span::styled("⎋ ", theme::primary()),
                Span::styled("Cancel", theme::muted()),
            ])),
            chunks[3],
        );
    } else {
        // Select list.
        let mut lines: Vec<Line<'static>> = vec![path_line, hint_line, Line::raw("")];
        for (i, c) in e.choices.iter().enumerate() {
            let active = i == e.select_index;
            let marker = if active { "● " } else { "○ " };
            let style = if active {
                Style::default().fg(theme::PRIMARY).add_modifier(Modifier::BOLD)
            } else {
                theme::muted()
            };
            lines.push(Line::from(vec![
                Span::raw(" "),
                Span::styled(marker, style),
                Span::styled(c.clone(), style),
            ]));
        }
        f.render_widget(Paragraph::new(lines), inner);
        // Footer.
        let footer_y = inner.y + inner.height.saturating_sub(1);
        f.render_widget(
            Paragraph::new(Line::from(vec![
                Span::raw(" "),
                Span::styled("↑↓ ", theme::primary()),
                Span::styled("Pick", theme::muted()),
                Span::raw("    "),
                Span::styled("⏎ ", theme::primary()),
                Span::styled("Save", theme::muted()),
                Span::raw("    "),
                Span::styled("⎋ ", theme::primary()),
                Span::styled("Cancel", theme::muted()),
            ])),
            Rect {
                x: inner.x,
                y: footer_y,
                width: inner.width,
                height: 1,
            },
        );
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

    // When the daemon isn't running we have no live metrics, so spell that
    // out instead of showing blank version/skills/CPU/RAM which historically
    // looked like a TUI bug rather than "daemon is off."
    if !st.alive {
        lines.push(Line::from(Span::styled(
            "  Daemon is not running. Start it with:",
            theme::muted(),
        )));
        lines.push(Line::from(vec![
            Span::raw("    "),
            Span::styled(
                "opsintelligence start",
                Style::default().fg(theme::PRIMARY).add_modifier(Modifier::BOLD),
            ),
        ]));
        lines.push(Line::raw(""));
    }

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

fn render_agents(agents: &[AgentInfo], hint: &str, query: &str) -> Vec<Line<'static>> {
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
        if hint.is_empty() {
            lines.push(Line::from(Span::styled(
                "No specialist agents have been spawned yet in this session.",
                theme::muted(),
            )));
        } else {
            lines.push(Line::from(Span::styled(hint.to_string(), theme::error_style())));
        }
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
