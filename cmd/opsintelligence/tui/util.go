package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// nz returns s trimmed, or fallback if the trimmed result is empty.
// Shared by the onboarding summary and other text rendering.
func nz(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

// clipDashboardLines truncates a multi-line block to maxLines, appending a
// muted "↓ N more lines" hint when truncation occurs.
func clipDashboardLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if maxLines <= 0 || len(lines) <= maxLines {
		return s
	}
	out := lines[:maxLines]
	out = append(out, lipgloss.NewStyle().Foreground(ColorMuted).
		Render(fmt.Sprintf("↓ %d more lines (resize terminal)", len(lines)-maxLines)))
	return strings.Join(out, "\n")
}

// max returns the larger of two ints. Pre-Go 1.21 compatibility shim previously
// lived in dashboard.go; kept here for the onboarding code path that uses it.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// minReplScanlineWidth was previously defined in repl.go; the setup banner
// still uses it to compute its scanline suffix width.
func minReplScanlineWidth(termW int) int {
	w := termW - 6
	if w < 24 {
		w = 24
	}
	if w > 72 {
		w = 72
	}
	return w
}
