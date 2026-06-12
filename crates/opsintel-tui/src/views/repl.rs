//! REPL view — port of `cmd/opsintelligence/tui/repl.go`.

use crate::protocol::{AgentDelta, AgentEnd, AgentError, ReplViewState};
use crate::theme;
use crate::widgets::{markdown, spinner::Spinner, textarea::{TextArea, TextAreaView}};
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Paragraph, Wrap},
    Frame,
};

#[derive(Clone)]
struct ToolEvent {
    name: String,
    input: String,
    result: String,
    pending: bool,
}

#[derive(Default)]
pub struct ReplView {
    pub state: ReplViewState,
    history: Vec<Line<'static>>, // committed lines
    token_buf: String,           // in-flight streaming text
    active_tools: Vec<ToolEvent>,
    thinking: bool,
    input: TextArea,
    sent: Vec<String>,
    history_idx: Option<usize>,
    scroll_offset: u16,
    auto_scroll: bool,
    spinner: Spinner,
    width: u16,
    height: u16,
    error_text: Option<String>,
    last_done: Option<AgentEnd>,
    /// Local accumulator of token usage across the session.
    session_usage_total: u64,
    session_usage_prompt: u64,
    session_usage_completion: u64,
}

/// Outbound action the parent app should perform.
pub enum Outbound {
    /// Send a user message to the Go core.
    Submit(String),
    /// Cancel an in-flight agent run.
    Cancel,
    /// Quit the application.
    Quit,
    /// No-op (e.g. local key handled).
    None,
}

impl ReplView {
    pub fn new(state: ReplViewState) -> Self {
        let mut v = Self {
            input: TextArea::new("Message OpsIntelligence…"),
            auto_scroll: true,
            spinner: Spinner::new(),
            ..Default::default()
        };
        // Banner from Go is rendered as muted prose lines.
        if !state.banner.is_empty() {
            for line in state.banner.lines() {
                v.history.push(Line::from(Span::styled(line.to_string(), theme::muted())));
            }
            v.history.push(Line::raw(""));
        }
        v.state = state;
        v
    }

    pub fn set_size(&mut self, width: u16, height: u16) {
        self.width = width;
        self.height = height;
    }

    pub fn apply_delta(&mut self, delta: AgentDelta) {
        match delta {
            AgentDelta::Token { text } => {
                self.thinking = true;
                self.token_buf.push_str(&text);
            }
            AgentDelta::ToolCall { name, input } => {
                if !self.token_buf.is_empty() {
                    self.flush_token();
                }
                self.active_tools.push(ToolEvent {
                    name,
                    input,
                    result: String::new(),
                    pending: true,
                });
            }
            AgentDelta::ToolResult { name, result } => {
                for te in self.active_tools.iter_mut().rev() {
                    if te.pending && te.name == name {
                        te.pending = false;
                        te.result = result;
                        break;
                    }
                }
            }
        }
        if self.auto_scroll {
            self.scroll_offset = u16::MAX;
        }
    }

    pub fn apply_end(&mut self, end: AgentEnd) {
        self.flush_token();
        // Commit completed tool blocks.
        for te in std::mem::take(&mut self.active_tools) {
            self.commit_tool_block(&te);
        }
        self.thinking = false;
        self.session_usage_total += end.usage.total_tokens;
        self.session_usage_prompt += end.usage.prompt_tokens;
        self.session_usage_completion += end.usage.completion_tokens;
        let summary = format!(
            "   ▸ {} iter · {} tok",
            end.iterations,
            fmt_num(end.usage.total_tokens)
        );
        self.history.push(Line::from(Span::styled(summary, theme::muted())));
        self.history.push(Line::raw(""));
        self.last_done = Some(end);
        if self.auto_scroll {
            self.scroll_offset = u16::MAX;
        }
    }

    pub fn apply_error(&mut self, err: AgentError) {
        self.flush_token();
        self.active_tools.clear();
        self.thinking = false;
        self.history.push(Line::from(vec![
            Span::styled("✗ error: ", theme::error_style()),
            Span::styled(err.message.clone(), theme::muted()),
        ]));
        self.history.push(Line::raw(""));
        self.error_text = Some(err.message);
    }

    fn flush_token(&mut self) {
        if self.token_buf.is_empty() {
            return;
        }
        let width = (self.width.saturating_sub(6)) as usize;
        let mut lines = markdown::render(&self.token_buf, width.max(20));
        if let Some(first) = lines.first_mut() {
            let mut spans = vec![Span::styled("› ", theme::primary())];
            spans.extend(first.spans.iter().cloned());
            *first = Line::from(spans);
        }
        self.history.extend(lines);
        self.token_buf.clear();
    }

    fn commit_tool_block(&mut self, te: &ToolEvent) {
        let mut top = vec![
            Span::styled("  › ", theme::muted()),
            Span::styled(te.name.clone(), theme::tool_badge()),
        ];
        if !te.input.is_empty() {
            top.push(Span::styled(format!(" {}", te.input), theme::muted()));
        }
        self.history.push(Line::from(top));
        if !te.result.is_empty() {
            let is_err = is_error_result(&te.result);
            let mark_style = if is_err { theme::error_style() } else { Style::default().fg(theme::SUCCESS) };
            let text_style = if is_err { theme::error_style() } else { theme::muted() };
            self.history.push(Line::from(vec![
                Span::styled("  › ", theme::muted()),
                Span::styled(if is_err { "✗" } else { "✓" }, mark_style),
                Span::raw(" "),
                Span::styled(te.result.clone(), text_style),
            ]));
        }
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> Outbound {
        if key.kind != KeyEventKind::Press {
            return Outbound::None;
        }

        // Global shortcuts.
        match key.code {
            KeyCode::Esc => return Outbound::Quit,
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                if self.thinking {
                    return Outbound::Cancel;
                }
                return Outbound::Quit;
            }
            KeyCode::Char('l') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                self.history.clear();
                self.token_buf.clear();
                self.active_tools.clear();
                self.scroll_offset = 0;
                return Outbound::None;
            }
            KeyCode::PageUp => {
                self.auto_scroll = false;
                self.scroll_offset = self.scroll_offset.saturating_sub(self.height / 2);
                return Outbound::None;
            }
            KeyCode::PageDown => {
                self.scroll_offset = self.scroll_offset.saturating_add(self.height / 2);
                return Outbound::None;
            }
            KeyCode::Enter => {
                if self.thinking {
                    return Outbound::None;
                }
                let line = self.input.value().trim().to_string();
                if line.is_empty() {
                    return Outbound::None;
                }
                self.sent.push(line.clone());
                self.history_idx = None;
                self.history.push(Line::from(vec![
                    Span::styled("You", Style::default().fg(theme::USER_MSG).add_modifier(Modifier::BOLD)),
                    Span::styled(" › ", theme::muted()),
                    Span::raw(line.clone()),
                ]));
                self.history.push(Line::raw(""));
                self.input.reset();
                self.thinking = true;
                self.token_buf.clear();
                self.active_tools.clear();
                self.auto_scroll = true;
                self.scroll_offset = u16::MAX;
                return Outbound::Submit(line);
            }
            KeyCode::Up => {
                if !self.thinking && self.input.is_empty() && !self.sent.is_empty() {
                    let new_idx = match self.history_idx {
                        None => self.sent.len() - 1,
                        Some(0) => 0,
                        Some(i) => i - 1,
                    };
                    self.history_idx = Some(new_idx);
                    self.input.set_value(self.sent[new_idx].clone());
                    return Outbound::None;
                }
                self.auto_scroll = false;
                self.scroll_offset = self.scroll_offset.saturating_sub(1);
                return Outbound::None;
            }
            KeyCode::Down => {
                if !self.thinking {
                    if let Some(i) = self.history_idx {
                        if i + 1 < self.sent.len() {
                            self.history_idx = Some(i + 1);
                            self.input.set_value(self.sent[i + 1].clone());
                        } else {
                            self.history_idx = None;
                            self.input.set_value(String::new());
                        }
                        return Outbound::None;
                    }
                }
                self.scroll_offset = self.scroll_offset.saturating_add(1);
                return Outbound::None;
            }
            _ => {}
        }

        // Otherwise delegate to the input widget.
        let _ = self.input.handle_key(key);
        Outbound::None
    }

    pub fn render(&mut self, f: &mut Frame, area: Rect) {
        self.width = area.width;
        self.height = area.height;

        // Outer frame.
        let inner = crate::widgets::chrome::outer_block(
            f,
            area,
            "OpsIntelligence",
            "REPL",
            None,
        );
        // Status pill: session ID short.
        let pill = format!(" {} ", short_id(&self.state.session_id));
        crate::widgets::chrome::render_pill(f, area, &pill, theme::PRIMARY);

        // Grow the Message panel with the wrapped input, capped so it never
        // eats the conversation. text width = inner − panel borders (2) −
        // "› " prefix (2). Panel height = input rows + 2 borders + status +
        // command bar.
        const MAX_INPUT_ROWS: u16 = 6;
        let text_w = inner.width.saturating_sub(4).max(8);
        let input_rows = self.input.display_rows(text_w).clamp(1, MAX_INPUT_ROWS);
        let panel_h = input_rows + 4;

        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Min(3),            // chat panel
                Constraint::Length(panel_h),   // input + status, grows with text
            ])
            .split(inner);

        self.render_chat(f, layout[0]);
        self.render_input(f, layout[1], input_rows);
    }

    fn render_chat(&mut self, f: &mut Frame, area: Rect) {
        let mut lines: Vec<Line<'static>> = self.history.clone();
        if !self.token_buf.is_empty() {
            let width = area.width.saturating_sub(4) as usize;
            let rendered = markdown::render(&self.token_buf, width.max(20));
            for (i, mut ln) in rendered.into_iter().enumerate() {
                if i == 0 {
                    let mut spans = vec![Span::styled("› ", theme::primary())];
                    spans.extend(ln.spans);
                    ln = Line::from(spans);
                }
                lines.push(ln);
            }
        }
        for te in &self.active_tools {
            let mut row = vec![
                Span::styled("  › ", theme::muted()),
                Span::styled(te.name.clone(), theme::tool_badge()),
            ];
            if !te.input.is_empty() {
                row.push(Span::styled(format!(" {}", te.input), theme::muted()));
            }
            if te.pending {
                row.push(Span::raw(" "));
                row.push(Span::styled(self.spinner.frame().to_string(), theme::neon()));
            }
            lines.push(Line::from(row));
            if !te.pending && !te.result.is_empty() {
                let is_err = is_error_result(&te.result);
                let mark_style = if is_err {
                    theme::error_style()
                } else {
                    Style::default().fg(theme::SUCCESS)
                };
                let text_style = if is_err { theme::error_style() } else { theme::muted() };
                lines.push(Line::from(vec![
                    Span::styled("  › ", theme::muted()),
                    Span::styled(if is_err { "✗" } else { "✓" }, mark_style),
                    Span::raw(" "),
                    Span::styled(te.result.clone(), text_style),
                ]));
            }
        }

        // Compute scroll: when auto_scroll, pin to bottom.
        let total = lines.len() as u16;
        let visible = area.height.saturating_sub(2); // border top + bottom
        let max_scroll = total.saturating_sub(visible);
        if self.auto_scroll || self.scroll_offset > max_scroll {
            self.scroll_offset = max_scroll;
        }

        let title = if self.thinking { "Conversation  ·  thinking…" } else { "Conversation" };
        let block = crate::widgets::chrome::panel_block(
            title,
            if self.thinking { theme::PRIMARY } else { theme::OUTLINE_VARIANT },
            self.thinking,
        );
        f.render_widget(
            Paragraph::new(lines)
                .block(block)
                .wrap(Wrap { trim: false })
                .scroll((self.scroll_offset, 0)),
            area,
        );
    }

    fn render_input(&self, f: &mut Frame, area: Rect, input_rows: u16) {
        // Input panel (bordered).
        let input_block = crate::widgets::chrome::panel_block(
            "Message",
            if self.thinking { theme::OUTLINE_VARIANT } else { theme::PRIMARY },
            !self.thinking,
        );
        let inner = input_block.inner(area);
        f.render_widget(input_block, area);

        let rows = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(input_rows), // input (grows + scrolls)
                Constraint::Length(1),           // status
                Constraint::Length(1),           // command bar
            ])
            .split(inner);

        // Input row: "› " prefix + textarea.
        let prefix_row = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Length(2), Constraint::Min(1)])
            .split(rows[0]);
        let prefix = Paragraph::new(Span::styled("› ", theme::primary()));
        f.render_widget(prefix, prefix_row[0]);
        let ta = TextAreaView {
            area: &self.input,
            style: Style::default().fg(theme::ON_SURFACE),
            placeholder_style: theme::muted(),
            focused: !self.thinking,
        };
        f.render_widget(ta, prefix_row[1]);

        // Status row + command bar.
        let status_line = if self.thinking {
            let mut spans = vec![
                Span::raw(" "),
                Span::styled(self.spinner.frame().to_string(), theme::neon()),
                Span::styled(" · ", theme::neon()),
                Span::styled("thinking", theme::muted()),
            ];
            if let Some(te) = self.active_tools.iter().rev().find(|t| t.pending) {
                spans.push(Span::raw("  "));
                spans.push(Span::styled(format!("⚡ {}", te.name), theme::tool_badge()));
                if !te.input.is_empty() {
                    spans.push(Span::styled(format!(" {}", te.input), theme::muted()));
                }
            }
            Line::from(spans)
        } else if !self.state.model_name.is_empty() {
            Line::from(vec![
                Span::raw(" "),
                Span::styled("model ", theme::muted()),
                Span::styled(
                    self.state.model_name.clone(),
                    Style::default().fg(theme::ON_SURFACE),
                ),
            ])
        } else {
            Line::raw("")
        };
        f.render_widget(Paragraph::new(status_line), rows[1]);

        let entries: &[(&str, &str)] = if self.thinking {
            &[("⌃C", "Cancel")]
        } else {
            &[
                ("⏎", "Send"),
                ("⌃J", "Newline"),
                ("↑", "Recall"),
                ("⌃L", "Clear"),
                ("⎋", "Quit"),
            ]
        };
        crate::widgets::chrome::render_command_bar(f, rows[2], entries);
    }
}

fn is_error_result(s: &str) -> bool {
    if s.is_empty() {
        return false;
    }
    let lo = s.to_ascii_lowercase();
    lo.starts_with("error:")
        || lo.starts_with("failed:")
        || lo.contains("cannot unmarshal")
        || (lo.contains("not found") && lo.starts_with("unknown"))
}

fn short_id(s: &str) -> String {
    if s.len() <= 8 {
        s.to_string()
    } else {
        s[..8].to_string()
    }
}

fn fmt_num(n: u64) -> String {
    let s = n.to_string();
    if s.len() <= 3 {
        return s;
    }
    let chars: Vec<char> = s.chars().collect();
    let mut out = String::with_capacity(s.len() + s.len() / 3);
    for (i, c) in chars.iter().enumerate() {
        if i > 0 && (chars.len() - i) % 3 == 0 {
            out.push(',');
        }
        out.push(*c);
    }
    out
}
