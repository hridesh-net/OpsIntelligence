//! Minimal single-line / multi-line text input. Ctrl+J inserts a newline.

use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    buffer::Buffer,
    layout::Rect,
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Paragraph, Widget, Wrap},
};

#[derive(Default)]
pub struct TextArea {
    buf: String,
    cursor: usize, // byte offset into buf
    placeholder: String,
}

impl TextArea {
    pub fn new(placeholder: impl Into<String>) -> Self {
        Self {
            placeholder: placeholder.into(),
            ..Default::default()
        }
    }

    pub fn value(&self) -> &str {
        &self.buf
    }

    pub fn set_value(&mut self, s: impl Into<String>) {
        self.buf = s.into();
        self.cursor = self.buf.len();
    }

    pub fn reset(&mut self) {
        self.buf.clear();
        self.cursor = 0;
    }

    pub fn is_empty(&self) -> bool {
        self.buf.is_empty()
    }

    /// Number of display rows the buffer occupies when hard-wrapped to `width`
    /// columns (newlines force a break). Always at least 1. Used by the host to
    /// grow the input panel so wrapped text stays visible.
    pub fn display_rows(&self, width: u16) -> u16 {
        let w = width.max(1) as usize;
        let mut rows: u16 = 1;
        let mut col = 0usize;
        for ch in self.buf.chars() {
            if ch == '\n' {
                rows = rows.saturating_add(1);
                col = 0;
                continue;
            }
            if col >= w {
                rows = rows.saturating_add(1);
                col = 0;
            }
            col += 1;
        }
        rows
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> bool {
        if key.kind != KeyEventKind::Press {
            return false;
        }
        match key.code {
            KeyCode::Char(c) => {
                if key.modifiers.contains(KeyModifiers::CONTROL) {
                    if c == 'j' || c == 'J' {
                        self.insert_char('\n');
                        return true;
                    }
                    return false;
                }
                self.insert_char(c);
                true
            }
            KeyCode::Backspace => {
                self.delete_left();
                true
            }
            KeyCode::Delete => {
                self.delete_right();
                true
            }
            KeyCode::Left => {
                self.move_left();
                true
            }
            KeyCode::Right => {
                self.move_right();
                true
            }
            KeyCode::Home => {
                self.cursor = 0;
                true
            }
            KeyCode::End => {
                self.cursor = self.buf.len();
                true
            }
            _ => false,
        }
    }

    fn insert_char(&mut self, c: char) {
        self.buf.insert(self.cursor, c);
        self.cursor += c.len_utf8();
    }

    fn delete_left(&mut self) {
        if self.cursor == 0 {
            return;
        }
        let prev = prev_char_boundary(&self.buf, self.cursor);
        self.buf.replace_range(prev..self.cursor, "");
        self.cursor = prev;
    }

    fn delete_right(&mut self) {
        if self.cursor >= self.buf.len() {
            return;
        }
        let next = next_char_boundary(&self.buf, self.cursor);
        self.buf.replace_range(self.cursor..next, "");
    }

    fn move_left(&mut self) {
        if self.cursor == 0 {
            return;
        }
        self.cursor = prev_char_boundary(&self.buf, self.cursor);
    }

    fn move_right(&mut self) {
        if self.cursor >= self.buf.len() {
            return;
        }
        self.cursor = next_char_boundary(&self.buf, self.cursor);
    }
}

fn prev_char_boundary(s: &str, i: usize) -> usize {
    let mut j = i.saturating_sub(1);
    while j > 0 && !s.is_char_boundary(j) {
        j -= 1;
    }
    j
}

fn next_char_boundary(s: &str, i: usize) -> usize {
    let mut j = (i + 1).min(s.len());
    while j < s.len() && !s.is_char_boundary(j) {
        j += 1;
    }
    j
}

pub struct TextAreaView<'a> {
    pub area: &'a TextArea,
    pub style: Style,
    pub placeholder_style: Style,
    pub focused: bool,
}

impl<'a> Widget for TextAreaView<'a> {
    fn render(self, rect: Rect, buf: &mut Buffer) {
        // Empty + unfocused → placeholder.
        if self.area.buf.is_empty() && !self.focused {
            Paragraph::new(Line::from(Span::styled(
                self.area.placeholder.clone(),
                self.placeholder_style,
            )))
            .wrap(Wrap { trim: false })
            .render(rect, buf);
            return;
        }

        let width = rect.width.max(1) as usize;
        let height = rect.height.max(1) as usize;
        let cursor = self.area.cursor.min(self.area.buf.len());
        let normal = self.style;
        let cursor_style = self.style.add_modifier(Modifier::REVERSED);

        // Hard-wrap the buffer into display rows ourselves so the cursor row is
        // known exactly — that lets us scroll to keep the caret on screen and
        // guarantees no character is clipped past the right edge. We don't use
        // Paragraph's Wrap here because it can't report the wrapped cursor row.
        let mut rows: Vec<String> = vec![String::new()];
        let mut col = 0usize;
        let mut cur_row = 0usize;
        let mut cur_col = 0usize;
        let mut placed = false;
        for (b, ch) in self.area.buf.char_indices() {
            if ch != '\n' && col >= width {
                rows.push(String::new());
                col = 0;
            }
            if b == cursor {
                cur_row = rows.len() - 1;
                cur_col = col;
                placed = true;
            }
            if ch == '\n' {
                rows.push(String::new());
                col = 0;
                continue;
            }
            rows.last_mut().unwrap().push(ch);
            col += 1;
        }
        if !placed {
            // Caret at end of buffer; it needs its own cell, wrapping if the
            // final row is already full.
            if col >= width {
                rows.push(String::new());
                col = 0;
            }
            cur_row = rows.len() - 1;
            cur_col = col;
        }

        // Vertical scroll so the cursor row stays within the visible window.
        let scroll = cur_row.saturating_sub(height.saturating_sub(1));

        let mut out: Vec<Line<'static>> = Vec::with_capacity(height);
        for (ri, row) in rows.iter().enumerate().skip(scroll).take(height) {
            let chars: Vec<char> = row.chars().collect();
            let mut spans: Vec<Span<'static>> = Vec::with_capacity(chars.len() + 1);
            for (ci, c) in chars.iter().enumerate() {
                let st = if self.focused && ri == cur_row && ci == cur_col {
                    cursor_style
                } else {
                    normal
                };
                spans.push(Span::styled(c.to_string(), st));
            }
            // Caret sitting just past the last char of its row.
            if self.focused && ri == cur_row && cur_col >= chars.len() {
                spans.push(Span::styled(" ".to_string(), cursor_style));
            }
            out.push(Line::from(spans));
        }

        Paragraph::new(out).render(rect, buf);
    }
}
