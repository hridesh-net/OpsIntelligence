package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

const stepWidth = 70

// OnboardTheme returns a huh theme consistent with the OpsIntelligence palette.
// Use this for all huh forms in the onboarding wizard.
func OnboardTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Title = lipgloss.NewStyle().Foreground(ColorNeon).Bold(true)
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	t.Focused.UnselectedOption = lipgloss.NewStyle().Foreground(ColorMuted)
	t.Focused.Description = lipgloss.NewStyle().Foreground(ColorMuted)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	t.Focused.TextInput.Text = lipgloss.NewStyle().Foreground(ColorWhite)
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	t.Blurred.Title = lipgloss.NewStyle().Foreground(ColorMuted)
	t.Blurred.TextInput.Text = lipgloss.NewStyle().Foreground(ColorMuted)
	return t
}

// ── Step Progress Header ──────────────────────────────────────────────────────

// PrintOnboardStep prints a styled step header before each wizard section.
// Call this before running a huh form to give users clear positional context.
func PrintOnboardStep(step, total int, icon, title, subtitle string) {
	// Step pill: e.g. "  1 / 10  "
	pill := lipgloss.NewStyle().
		Background(ColorPrimary).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true).
		Padding(0, 1).
		Render(fmt.Sprintf(" %d / %d ", step, total))

	// Progress bar: filled up to previous step
	pct := float64(step-1) / float64(total) * 100
	bar := ProgressBarLavender(pct, 16)

	// Title (neon + icon)
	titleStr := lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).
		Render(icon + "  " + title)

	// Assemble top row
	topRow := "  " + pill + "  " + bar + "  " + titleStr

	// Subtitle in muted
	subRow := ""
	if subtitle != "" {
		subRow = lipgloss.NewStyle().Foreground(ColorMuted).PaddingLeft(4).
			Render(subtitle)
	}

	// Bottom scanner line
	scanLen := stepWidth
	if pw := lipgloss.Width(topRow); pw > scanLen {
		scanLen = pw
	}
	scanLine := lipgloss.NewStyle().Foreground(ColorBorder).
		Render("  " + strings.Repeat("╌", scanLen-2))

	fmt.Println()
	fmt.Println(topRow)
	if subRow != "" {
		fmt.Println(subRow)
	}
	fmt.Println(scanLine)
	fmt.Println()
}

// ── Status Lines ─────────────────────────────────────────────────────────────

// PrintOnboardSuccess prints a styled ✓ success line.
func PrintOnboardSuccess(msg string) {
	check := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render("  ✓")
	text := lipgloss.NewStyle().Foreground(ColorWhite).Render("  " + msg)
	fmt.Println(check + text)
}

// PrintOnboardWarn prints a styled ⚠ warning line.
func PrintOnboardWarn(msg string) {
	icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#F9A825")).Bold(true).Render("  ⚠")
	text := lipgloss.NewStyle().Foreground(ColorMuted).Render("  " + msg)
	fmt.Println(icon + text)
}

// PrintOnboardInfo prints a styled › info line.
func PrintOnboardInfo(msg string) {
	icon := lipgloss.NewStyle().Foreground(ColorPrimary).Render("  ›")
	text := lipgloss.NewStyle().Foreground(ColorMuted).Render("  " + msg)
	fmt.Println(icon + text)
}

// PrintOnboardError prints a styled ✗ error line.
func PrintOnboardError(msg string) {
	icon := lipgloss.NewStyle().Foreground(ColorError).Bold(true).Render("  ✗")
	text := lipgloss.NewStyle().Foreground(ColorError).Render("  " + msg)
	fmt.Println(icon + text)
}

// ── Token Reveal ──────────────────────────────────────────────────────────────

// PrintOnboardGeneratedToken renders a styled reveal box for an auto-generated secret.
func PrintOnboardGeneratedToken(label, token string) {
	icon := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("🔑 ")
	head := lipgloss.NewStyle().Foreground(ColorMuted).Render(label + " ")
	tok := lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).Render(token)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccentLavender).
		Background(ColorSurface).
		Padding(0, 2).
		Render(icon + head + tok)

	fmt.Println()
	fmt.Println("  " + box)
	fmt.Println()
}

// ── Save Confirmation ─────────────────────────────────────────────────────────

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

// ── Animated Boot Lines ───────────────────────────────────────────────────────

// PrintOnboardBootLine prints a single animated "boot" line with a brief pause.
// Use sparingly between heavyweight operations for visual rhythm.
func PrintOnboardBootLine(msg string) {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼"}
	for i, f := range frames {
		spin := lipgloss.NewStyle().Foreground(ColorPrimary).Render(f)
		label := lipgloss.NewStyle().Foreground(ColorMuted).Render("  " + msg)
		fmt.Printf("\r%s%s", spin, label)
		if i < len(frames)-1 {
			time.Sleep(60 * time.Millisecond)
		}
	}
	check := lipgloss.NewStyle().Foreground(ColorCyan).Render("✓")
	label := lipgloss.NewStyle().Foreground(ColorMuted).Render("  " + msg)
	fmt.Printf("\r%s%s\n", check, label)
}

// PrintOnboardWelcomeSubtitle prints an animated char-by-char subtitle line.
// Keeps each character print tight enough that it won't flicker badly.
func PrintOnboardWelcomeSubtitle(msg string) {
	style := lipgloss.NewStyle().Foreground(ColorMuted).Faint(true)
	fmt.Print("  ")
	for _, ch := range msg {
		fmt.Print(style.Render(string(ch)))
		time.Sleep(12 * time.Millisecond)
	}
	fmt.Println()
}
