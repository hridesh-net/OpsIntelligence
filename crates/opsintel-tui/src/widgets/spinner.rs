//! Simple dot spinner matching the bubbles Dot frames.

use std::time::Instant;

const FRAMES: &[&str] = &["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

pub struct Spinner {
    start: Instant,
}

impl Spinner {
    pub fn new() -> Self {
        Self { start: Instant::now() }
    }

    pub fn frame(&self) -> &'static str {
        let elapsed = self.start.elapsed().as_millis() / 80;
        FRAMES[(elapsed as usize) % FRAMES.len()]
    }
}

impl Default for Spinner {
    fn default() -> Self {
        Self::new()
    }
}
