//! Minimal markdown → ratatui Lines renderer.
//!
//! Ports the subset handled by `cmd/opsintelligence/tui/repl.go::renderMarkdown`:
//! - fenced code blocks (```lang)
//! - inline `code`
//! - **bold**
//! - # / ## headers
//! - prose word-wrap respecting leading indent

use crate::theme;
use ratatui::{
    style::{Modifier, Style},
    text::{Line, Span},
};

pub fn render(text: &str, max_width: usize) -> Vec<Line<'static>> {
    let mut out = Vec::new();
    let mut in_code = false;

    for line in text.split('\n') {
        if let Some(rest) = line.strip_prefix("```") {
            if !in_code {
                in_code = true;
                let hint = if rest.is_empty() { "code" } else { rest };
                out.push(Line::from(vec![
                    Span::styled("  › ", theme::muted()),
                    Span::styled(hint.to_string(), theme::tool_badge()),
                ]));
            } else {
                in_code = false;
                out.push(Line::from(Span::styled("  › ────", theme::muted())));
            }
            continue;
        }
        if in_code {
            out.push(Line::from(vec![
                Span::styled("  │ ", theme::muted()),
                Span::styled(line.to_string(), Style::default().fg(theme::CODE)),
            ]));
            continue;
        }
        if let Some(rest) = line.strip_prefix("## ") {
            out.push(Line::from(Span::styled(
                rest.to_string(),
                theme::primary().add_modifier(Modifier::BOLD),
            )));
            continue;
        }
        if let Some(rest) = line.strip_prefix("# ") {
            out.push(Line::from(Span::styled(
                rest.to_string(),
                theme::neon().add_modifier(Modifier::BOLD),
            )));
            continue;
        }

        let prose = if max_width > 8 && line.chars().count() > max_width {
            wrap_plain(line, max_width)
        } else {
            vec![line.to_string()]
        };
        for wl in prose {
            out.push(Line::from(inline_spans(&wl)));
        }
    }
    out
}

/// Parses **bold** and `code` spans within a line.
fn inline_spans(line: &str) -> Vec<Span<'static>> {
    let mut spans: Vec<Span<'static>> = Vec::new();
    let mut buf = String::new();
    let bytes = line.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        // **bold**
        if i + 1 < bytes.len() && bytes[i] == b'*' && bytes[i + 1] == b'*' {
            if let Some(end) = find_subseq(&bytes[i + 2..], b"**") {
                if !buf.is_empty() {
                    spans.push(Span::raw(std::mem::take(&mut buf)));
                }
                let inner = &line[i + 2..i + 2 + end];
                spans.push(Span::styled(
                    inner.to_string(),
                    Style::default()
                        .fg(theme::ON_SURFACE)
                        .add_modifier(Modifier::BOLD),
                ));
                i += 2 + end + 2;
                continue;
            }
        }
        // `code`
        if bytes[i] == b'`' {
            if let Some(end) = find_subseq(&bytes[i + 1..], b"`") {
                if !buf.is_empty() {
                    spans.push(Span::raw(std::mem::take(&mut buf)));
                }
                let inner = &line[i + 1..i + 1 + end];
                spans.push(Span::styled(
                    inner.to_string(),
                    Style::default().fg(theme::CODE).bg(theme::SURFACE),
                ));
                i += 1 + end + 1;
                continue;
            }
        }
        // copy one byte; safe because we only break on ASCII control chars.
        let ch_len = utf8_char_len(bytes[i]);
        buf.push_str(&line[i..i + ch_len.min(line.len() - i)]);
        i += ch_len;
    }
    if !buf.is_empty() {
        spans.push(Span::raw(buf));
    }
    if spans.is_empty() {
        spans.push(Span::raw(String::new()));
    }
    spans
}

fn find_subseq(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    if needle.is_empty() || haystack.len() < needle.len() {
        return None;
    }
    (0..=haystack.len() - needle.len()).find(|&i| &haystack[i..i + needle.len()] == needle)
}

fn utf8_char_len(first: u8) -> usize {
    if first < 0x80 {
        1
    } else if first < 0xC0 {
        1 // invalid continuation, fall back so we don't loop
    } else if first < 0xE0 {
        2
    } else if first < 0xF0 {
        3
    } else {
        4
    }
}

fn wrap_plain(line: &str, max_w: usize) -> Vec<String> {
    let trimmed: String = line.chars().take_while(|c| *c == ' ' || *c == '\t').collect();
    let indent = trimmed;
    let rest = &line[indent.len()..];

    let effective = max_w.saturating_sub(indent.chars().count()).max(10);
    let mut out = Vec::new();
    let mut cur = String::new();
    let mut first = true;

    for word in rest.split_whitespace() {
        if cur.is_empty() {
            cur.push_str(word);
        } else if cur.chars().count() + 1 + word.chars().count() <= effective {
            cur.push(' ');
            cur.push_str(word);
        } else {
            let line = if first { format!("{}{}", indent, cur) } else { cur.clone() };
            out.push(line);
            cur = word.to_string();
            first = false;
        }
    }
    if !cur.is_empty() {
        out.push(if first { format!("{}{}", indent, cur) } else { cur });
    }
    if out.is_empty() {
        out.push(line.to_string());
    }
    out
}
