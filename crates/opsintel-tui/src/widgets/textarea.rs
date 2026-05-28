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
        if self.area.buf.is_empty() && !self.focused {
            let p = Paragraph::new(Line::from(Span::styled(
                self.area.placeholder.clone(),
                self.placeholder_style,
            )))
            .wrap(Wrap { trim: false });
            p.render(rect, buf);
            return;
        }
        if self.area.buf.is_empty() {
            // Just a cursor block.
            let cursor = Span::styled(" ", self.style.add_modifier(Modifier::REVERSED));
            let p = Paragraph::new(Line::from(vec![cursor])).wrap(Wrap { trim: false });
            p.render(rect, buf);
            return;
        }

        let cursor = self.area.cursor.min(self.area.buf.len());
        let before = &self.area.buf[..cursor];
        let after = &self.area.buf[cursor..];

        let mut lines: Vec<Line<'static>> = Vec::new();
        let mut current: Vec<Span<'static>> = Vec::new();

        // Emit "before" text, splitting on '\n'.
        for ch in before.chars() {
            if ch == '\n' {
                lines.push(Line::from(std::mem::take(&mut current)));
            } else {
                current.push(Span::styled(ch.to_string(), self.style));
            }
        }

        // Insert cursor.
        let mut after_chars = after.chars();
        let cursor_char = after_chars.next();
        if self.focused {
            let s = self.style.add_modifier(Modifier::REVERSED);
            match cursor_char {
                Some('\n') => {
                    current.push(Span::styled(" ".to_string(), s));
                    lines.push(Line::from(std::mem::take(&mut current)));
                }
                Some(c) => current.push(Span::styled(c.to_string(), s)),
                None => current.push(Span::styled(" ".to_string(), s)),
            }
        } else if let Some(c) = cursor_char {
            if c == '\n' {
                lines.push(Line::from(std::mem::take(&mut current)));
            } else {
                current.push(Span::styled(c.to_string(), self.style));
            }
        }

        // Remaining "after" text.
        for ch in after_chars {
            if ch == '\n' {
                lines.push(Line::from(std::mem::take(&mut current)));
            } else {
                current.push(Span::styled(ch.to_string(), self.style));
            }
        }
        if !current.is_empty() {
            lines.push(Line::from(current));
        }

        let p = Paragraph::new(lines).wrap(Wrap { trim: false });
        p.render(rect, buf);
    }
}
