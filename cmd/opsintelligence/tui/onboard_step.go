package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// PrintOnboardSaved renders a completion banner after config is written.
func PrintOnboardSaved(configPath string) {
	inner := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render("  ✓  Configuration saved"),
		lipgloss.NewStyle().Foreground(ColorMuted).Render("     "+configPath),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorCyan).
		Background(ColorSurface).
		Padding(0, 1).
		Render(inner)
	fmt.Println()
	fmt.Println("  " + box)
	fmt.Println()
}

// PrintOnboardWelcomeSubtitle prints an animated char-by-char subtitle line.
func PrintOnboardWelcomeSubtitle(msg string) {
	style := lipgloss.NewStyle().Foreground(ColorMuted).Faint(true)
	fmt.Print("  ")
	for _, ch := range msg {
		fmt.Print(style.Render(string(ch)))
		time.Sleep(12 * time.Millisecond)
	}
	fmt.Println()
}
