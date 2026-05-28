//! Color palette ported from `cmd/opsintelligence/tui/theme.go`.
//!
//! Source uses `lipgloss.AdaptiveColor{Light, Dark}` — we pick the Dark variant
//! by default; light-mode detection is deferred. Colors are stored as
//! `ratatui::style::Color::Rgb` for terminals that support truecolor; fallback
//! to 256-color is automatic in crossterm.

use ratatui::style::{Color, Modifier, Style};

const fn rgb(r: u8, g: u8, b: u8) -> Color {
    Color::Rgb(r, g, b)
}

// ── Semantic palette (dark variant) ───────────────────────────────────────
pub const BACKGROUND: Color = rgb(0x1e, 0x1c, 0x1a);
pub const SURFACE: Color = rgb(0x25, 0x23, 0x21);
pub const CHROME_BG: Color = rgb(0x30, 0x2e, 0x2b);
pub const ON_SURFACE: Color = rgb(0xed, 0xe9, 0xe4);
pub const MUTED: Color = rgb(0x8a, 0x86, 0x80);
pub const EMPHASIS: Color = rgb(0xf1, 0xf1, 0xef);
pub const OUTLINE: Color = rgb(0x5d, 0x5f, 0x5d);
pub const OUTLINE_VARIANT: Color = rgb(0x3a, 0x3b, 0x38);

pub const BRAND_ACCENT: Color = rgb(0xff, 0x70, 0x43); // Modern Orange
pub const BRAND_ACCENT_SOFT: Color = rgb(0xff, 0x95, 0x75);

pub const SUCCESS: Color = rgb(0x4c, 0xaf, 0x7d);
pub const ERROR_COLOR: Color = rgb(0xba, 0x1a, 0x1a);
pub const WARN: Color = rgb(0xe6, 0xb8, 0x00);

// Legacy aliases (match theme.go).
pub const PRIMARY: Color = BRAND_ACCENT;
pub const NEON: Color = BRAND_ACCENT_SOFT;
pub const BORDER: Color = OUTLINE_VARIANT;
pub const USER_MSG: Color = BRAND_ACCENT;

// ── Style helpers ─────────────────────────────────────────────────────────
pub fn muted() -> Style {
    Style::default().fg(MUTED)
}

pub fn primary() -> Style {
    Style::default().fg(PRIMARY).add_modifier(Modifier::BOLD)
}

pub fn neon() -> Style {
    Style::default().fg(NEON).add_modifier(Modifier::BOLD)
}

pub fn error_style() -> Style {
    Style::default().fg(ERROR_COLOR).add_modifier(Modifier::BOLD)
}

pub fn tool_badge() -> Style {
    Style::default().fg(NEON).add_modifier(Modifier::BOLD)
}

pub fn header() -> Style {
    Style::default().fg(EMPHASIS).add_modifier(Modifier::BOLD)
}
