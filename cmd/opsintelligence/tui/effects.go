package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PulseBorder returns alternating border colors for a subtle "live" frame.
func PulseBorder(frame int) lipgloss.TerminalColor {
	if frame%2 == 0 {
		return ColorPrimary
	}
	return ColorNeon
}

// GradientWord renders a short string with a warm orange sweep across characters.
// The sweep goes muted → orange → soft orange for an editorial brand feel;
// stops adapt to light and dark terminal backgrounds.
func GradientWord(s string) string {
	if s == "" {
		return ""
	}
	palette := []lipgloss.TerminalColor{
		ColorMuted,
		lipgloss.AdaptiveColor{Light: "#D05A2E", Dark: "#D05A2E"},
		ColorPrimary,
		ColorNeon,
		ColorPrimary,
		lipgloss.AdaptiveColor{Light: "#D05A2E", Dark: "#D05A2E"},
		ColorMuted,
	}
	var b strings.Builder
	i := 0
	for _, r := range s {
		c := palette[i%len(palette)]
		b.WriteString(lipgloss.NewStyle().Foreground(c).Bold(true).Render(string(r)))
		i++
	}
	return b.String()
}

// CyberBracket wraps text in a clean angular bracket frame.
func CyberBracket(inner string) string {
	left := lipgloss.NewStyle().Foreground(ColorBracketMuted).Render("⟨")
	right := lipgloss.NewStyle().Foreground(ColorBracketMuted).Render("⟩")
	mid := lipgloss.NewStyle().Foreground(ColorOnSurface).Bold(true).Render(inner)
	return left + mid + right
}

// ScanlineSuffix adds a dim warm noise strip of rune width `width`.
func ScanlineSuffix(width int) string {
	if width < 8 {
		width = 32
	}
	raw := strings.Repeat("·░", (width+1)/2)
	rs := []rune(raw)
	if len(rs) > width {
		rs = rs[:width]
	}
	return lipgloss.NewStyle().Foreground(ColorBorder).Render(string(rs))
}

// RenderVersionBlock is output for `opsintelligence version` (TTY).
func RenderVersionBlock(ver string) string {
	line1 := GradientWord("OPSINTELLIGENCE") + "  " + lipgloss.NewStyle().Foreground(ColorMuted).Render(ver)
	line2 := CyberBracket("edge neural shell")
	line3 := ScanlineSuffix(48)
	return line1 + "\n" + line2 + "\n" + line3 + "\n"
}
