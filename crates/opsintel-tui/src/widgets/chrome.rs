//! Shared chrome widgets — outer frame, section panels, status pill,
//! bottom command bar. Used by every view to keep the look consistent.

use crate::theme;
use ratatui::{
    layout::{Alignment, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, BorderType, Borders, Paragraph},
    Frame,
};

/// Outer frame: single border, primary color, brand + section embedded in
/// the top-left of the border. Optional progress bar runs after the section
/// name. The returned `Rect` is the inner area for body content.
pub fn outer_block(
    f: &mut Frame,
    area: Rect,
    brand: &str,
    section: &str,
    progress_pct: Option<f32>,
) -> Rect {
    // Paint the entire frame area with the theme background first. Without
    // this, anything that was on the terminal before the TUI entered the
    // alt-screen bleeds through any region the outer block doesn't draw
    // pixels in (corners, gaps, transparent cells), which is why the Done
    // screen — and other views — looked like garbled half-renders.
    f.render_widget(
        Block::default().style(Style::default().bg(theme::BACKGROUND)),
        area,
    );

    let mut title_spans: Vec<Span<'static>> = vec![
        Span::raw("─ "),
        Span::styled(
            brand.to_string(),
            Style::default().fg(theme::PRIMARY).add_modifier(Modifier::BOLD),
        ),
    ];
    if !section.is_empty() {
        title_spans.push(Span::styled("  ·  ", theme::muted()));
        title_spans.push(Span::styled(
            section.to_string(),
            Style::default().fg(theme::ON_SURFACE).add_modifier(Modifier::BOLD),
        ));
    }
    if let Some(pct) = progress_pct {
        title_spans.push(Span::raw("   "));
        title_spans.push(Span::styled(
            progress_bar(pct, 18),
            Style::default().fg(theme::PRIMARY),
        ));
    }
    title_spans.push(Span::raw(" "));

    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        // Neutral outer border; the brand name in the title carries the accent.
        .border_style(Style::default().fg(theme::OUTLINE))
        .style(Style::default().bg(theme::BACKGROUND))
        .title(Line::from(title_spans))
        .title_alignment(Alignment::Left);
    let inner = block.inner(area);
    f.render_widget(block, area);
    inner
}

/// Status pill rendered into the top-right corner of `outer`. Typical use:
/// `render_pill(f, outer, " 03 / 14 ", theme::PRIMARY)`.
pub fn render_pill(f: &mut Frame, outer: Rect, label: &str, bg: ratatui::style::Color) {
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
        .bg(bg)
        .fg(theme::BACKGROUND)
        .add_modifier(Modifier::BOLD);
    f.render_widget(Paragraph::new(Span::styled(label.to_string(), style)), r);
}

/// Section panel: single border with a section name embedded in the top-left.
/// `accent` controls the title + border highlight color (typically PRIMARY
/// for the active panel, OUTLINE_VARIANT for inactive).
pub fn panel_block(name: &str, accent: ratatui::style::Color, primary_border: bool) -> Block<'static> {
    // Borders stay neutral; focus/active state is signalled by the title colour
    // (the accent) and a slightly brighter outline — not a full orange frame.
    let border = if primary_border { theme::OUTLINE } else { theme::OUTLINE_VARIANT };
    let title = Line::from(vec![
        Span::styled("─ ", Style::default().fg(border)),
        Span::styled(
            name.to_string(),
            Style::default()
                .fg(if primary_border { accent } else { theme::MUTED })
                .add_modifier(if primary_border { Modifier::BOLD } else { Modifier::empty() }),
        ),
        Span::raw(" "),
    ]);
    Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Plain)
        .border_style(Style::default().fg(border))
        // Fill the panel interior with the theme background so prior alt-screen
        // contents can't show through gaps in the body content.
        .style(Style::default().bg(theme::BACKGROUND))
        .title(title)
}

/// Bottom command bar: each entry is rendered as a colored key pill followed
/// by the action label. Pass slices of `(key, label)` pairs.
pub fn render_command_bar(f: &mut Frame, area: Rect, entries: &[(&str, &str)]) {
    if entries.is_empty() {
        return;
    }
    let mut spans: Vec<Span<'static>> = vec![Span::raw(" ")];
    for (i, (key, label)) in entries.iter().enumerate() {
        if i > 0 {
            spans.push(Span::styled("   ", theme::muted()));
        }
        spans.push(Span::styled(
            format!(" {} ", key),
            // Neutral key cap with an accent glyph — not a row of orange chips.
            Style::default()
                .bg(theme::CHROME_BG)
                .fg(theme::BRAND_ACCENT_SOFT)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" "));
        spans.push(Span::styled(label.to_string(), theme::muted()));
    }
    f.render_widget(Paragraph::new(Line::from(spans)), area);
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
