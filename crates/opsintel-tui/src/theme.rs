//! Color palette ported from `cmd/opsintelligence/tui/theme.go`.
//!
//! Source uses `lipgloss.AdaptiveColor{Light, Dark}` — we pick the Dark variant
//! by default; light-mode detection is deferred. Colors are stored as
//! `ratatui::style::Color::Rgb` for terminals that support truecolor; fallback
//! to 256-color is automatic in crossterm.
//!
//! Some constants are unused by current call sites but kept for symmetry with
//! the source Go palette so adding views later is mechanical.
#![allow(dead_code)]

use ratatui::style::{Color, Modifier, Style};

const fn rgb(r: u8, g: u8, b: u8) -> Color {
    Color::Rgb(r, g, b)
}

// ── Semantic palette (cool dark — neutrals are slate, not warm brown, so the
// orange accent reads as a sparse highlight instead of washing the whole UI) ──
pub const BACKGROUND: Color = rgb(0x15, 0x17, 0x1c);
pub const SURFACE: Color = rgb(0x1c, 0x1f, 0x27);
pub const CHROME_BG: Color = rgb(0x23, 0x27, 0x31);
pub const ON_SURFACE: Color = rgb(0xe7, 0xed, 0xf5);
pub const MUTED: Color = rgb(0x84, 0x90, 0xa2);
pub const EMPHASIS: Color = rgb(0xf2, 0xf5, 0xfa);
pub const OUTLINE: Color = rgb(0x47, 0x4f, 0x5e);
pub const OUTLINE_VARIANT: Color = rgb(0x2a, 0x2f, 0x3a);

pub const BRAND_ACCENT: Color = rgb(0xff, 0x70, 0x43); // Orange — used sparingly
pub const BRAND_ACCENT_SOFT: Color = rgb(0xff, 0x95, 0x75);

pub const SUCCESS: Color = rgb(0x46, 0xc9, 0x8b);
pub const ERROR_COLOR: Color = rgb(0xf0, 0x65, 0x5a);
pub const WARN: Color = rgb(0xe6, 0xb8, 0x00);
pub const CODE: Color = rgb(0x7c, 0xc7, 0xd9); // soft cyan for `code` (cool, not orange)

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
