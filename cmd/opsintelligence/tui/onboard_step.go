package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

const stepWidth = 70

// applyOpsHuhTheme applies brand adaptive colors to a huh theme built from ThemeBase().
// Shared by onboarding and setup wizard.
func applyOpsHuhTheme(t *huh.Theme) {
	button := lipgloss.NewStyle().Padding(0, 2).MarginRight(1)

	// Form.Base uses ColorSurface so the form card sits visually above the
	// page canvas (ColorBackground), mirroring the website's card-on-cream pattern.
	t.Form.Base = lipgloss.NewStyle().Foreground(ColorOnSurface).Background(ColorSurface)
	t.Group.Base = lipgloss.NewStyle().Foreground(ColorOnSurface).Background(ColorSurface)
	t.Group.Title = lipgloss.NewStyle().Foreground(ColorEmphasis).Bold(true)
	t.Group.Description = lipgloss.NewStyle().Foreground(ColorMuted)

	t.Focused.Base = t.Focused.Base.BorderForeground(ColorOutline).Foreground(ColorOnSurface).Background(ColorSurface)
	t.Focused.Card = t.Focused.Card.BorderForeground(ColorOutline)
	t.Focused.Title = lipgloss.NewStyle().Foreground(ColorEmphasis).Bold(true)
	t.Focused.NoteTitle = lipgloss.NewStyle().Foreground(ColorEmphasis).Bold(true).MarginBottom(1)
	t.Focused.Description = lipgloss.NewStyle().Foreground(ColorMuted)
	t.Focused.ErrorIndicator = lipgloss.NewStyle().Foreground(ColorError).SetString(" *")
	t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(ColorError).SetString(" *")
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(ColorPrimary).SetString("> ")
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(ColorPrimary)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(ColorPrimary)
	t.Focused.Option = lipgloss.NewStyle().Foreground(ColorOnSurface)
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	t.Focused.UnselectedOption = lipgloss.NewStyle().Foreground(ColorMuted)
	t.Focused.Directory = lipgloss.NewStyle().Foreground(ColorSecondary)
	t.Focused.File = lipgloss.NewStyle().Foreground(ColorOnSurface)
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(ColorPrimary).SetString("> ")
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(ColorSuccess).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(ColorMuted).SetString("• ")
	t.Focused.FocusedButton = button.Foreground(ColorOnAccent).Background(ColorPrimary)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = button.Foreground(ColorOnSurface).Background(ColorOutlineVariant)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	t.Focused.TextInput.Text = lipgloss.NewStyle().Foreground(ColorOnSurface)
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(ColorPrimary)
	t.Focused.TextInput.CursorText = lipgloss.NewStyle().Foreground(ColorOnSurface)
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(ColorMuted)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Blurred.Title = lipgloss.NewStyle().Foreground(ColorMuted)
	t.Blurred.TextInput.Text = lipgloss.NewStyle().Foreground(ColorMuted)
}

// OnboardTheme returns a huh theme consistent with the OpsIntelligence palette.
// Use this for all huh forms in the onboarding wizard.
func OnboardTheme() *huh.Theme {
	t := huh.ThemeBase()
	applyOpsHuhTheme(t)
	return t
}

// ── Step Progress Header ──────────────────────────────────────────────────────

// PrintOnboardOverallProgress prints one full-width line for the whole wizard.
// completed is how many steps are already finished (0..total). When entering
// step N, pass completed=N-1 so the bar reflects work done so far; pass
// completed=total when the wizard is finished.
func PrintOnboardOverallProgress(completed, total int) {
	if total <= 0 {
		return
	}
	if completed < 0 {
		completed = 0
	}
	if completed > total {
		completed = total
	}
	pct := float64(completed) / float64(total) * 100
	bar := ProgressBarLavender(pct, 36)
	label := fmt.Sprintf("%d / %d steps complete", completed, total)
	if completed >= total {
		label = "All steps complete"
	}
	labelStyled := lipgloss.NewStyle().Foreground(ColorMuted).Render(label)
	line := lipgloss.JoinHorizontal(lipgloss.Left, "  ", bar, "  ", labelStyled)
	fmt.Println(line)
}

// PrintOnboardStep prints a styled step header before each wizard section.
// Call this before running a huh form to give users clear positional context.
// Overall progress uses a single wide bar (see PrintOnboardOverallProgress), not a per-header mini-bar.
func PrintOnboardStep(step, total int, icon, title, subtitle string) {
	PrintOnboardOverallProgress(step-1, total)

	// Step pill: e.g. "  1 / 10  "
	pill := lipgloss.NewStyle().
		Background(ColorPrimary).
		Foreground(ColorOnAccent).
		Bold(true).
		Padding(0, 1).
		Render(fmt.Sprintf(" %d / %d ", step, total))

	// Title (neon + icon)
	titleStr := lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).
		Render(icon + "  " + title)

	// Assemble top row (no second progress bar — overall bar is the line above)
	topRow := "  " + pill + "  " + titleStr

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
	icon := lipgloss.NewStyle().Foreground(ColorWarn).Bold(true).Render("  ⚠")
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
