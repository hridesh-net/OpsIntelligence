package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderBrandMarkArt returns a neutral dotted frame (no mascot / claw glyph).
func renderBrandMarkArt() string {
	const inner = 16                           // spaces between vertical rules
	top := "  " + strings.Repeat("·", inner+2) // top rule
	side := "  ·" + strings.Repeat(" ", inner) + "·"
	sty := lipgloss.NewStyle().Foreground(ColorMuted)
	return sty.Render(top + "\n" + side + "\n" + side + "\n" + side + "\n" + top + "\n")
}

// RenderBanner renders the full splash banner: brand mark + info block side by side.
func RenderBanner(ver, sessionID string, providers, skillsCount int) string {
	markBlock := renderBrandMarkArt()

	subStyle := lipgloss.NewStyle().Foreground(ColorPrimary)
	dimStyle := lipgloss.NewStyle().Foreground(ColorMuted)

	infoLines := []string{
		"",
		"  " + GradientWord("OPSINTELLIGENCE") + " " + Muted.Render(ver),
		subStyle.Render("  Edge Intelligence System"),
		"",
		dimStyle.Render("  session  ") + Primary.Render(shortID(sessionID)),
		dimStyle.Render("  providers") + Primary.Render(fmt.Sprintf("  %d", providers)),
		dimStyle.Render("  skills   ") + Primary.Render(fmt.Sprintf("  %d", skillsCount)),
		"",
		Muted.Render("  Type your message, Enter to send"),
		Muted.Render("  ESC or Ctrl+C to quit"),
		Muted.Render("  opsintelligence guides github  —  GitHub / webhook creds"),
	}

	var infoSB strings.Builder
	for _, l := range infoLines {
		infoSB.WriteString(l + "\n")
	}

	combined := lipgloss.JoinHorizontal(lipgloss.Top,
		markBlock,
		infoSB.String(),
	)

	banner := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(0, 1).
		Render(combined)

	tagW := 56
	tagline := lipgloss.NewStyle().Foreground(ColorMuted).PaddingLeft(2).
		Render(strings.Repeat("·", tagW))

	return "\n" + banner + "\n" + tagline + "\n"
}

// PrintBanner prints the splash banner to stdout.
func PrintBanner(ver, sessionID string, providers, skillsCount int) {
	fmt.Println(RenderBanner(ver, sessionID, providers, skillsCount))
}

// PrintOnboardBanner prints a shorter version for the onboarding wizard.
func PrintOnboardBanner(ver string) {
	markBlock := renderBrandMarkArt()

	infoLines := []string{
		"",
		"  " + GradientWord("OPSINTELLIGENCE") + " " + Muted.Render(ver),
		Primary.Render("  Setup Wizard"),
		"",
		Muted.Render("  Let's get you configured."),
		Muted.Render("  This takes about 2 minutes."),
		"",
	}
	var infoSB strings.Builder
	for _, l := range infoLines {
		infoSB.WriteString(l + "\n")
	}

	combined := lipgloss.JoinHorizontal(lipgloss.Top, markBlock, infoSB.String())
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(0, 1).
		Render(combined)

	fmt.Println("\n" + box + "\n")
}

// shortID truncates a session ID to 8 chars for display.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
