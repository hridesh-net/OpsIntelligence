// Package tui provides the OpsIntelligence terminal UI components.
// Color palette: cool slate base with blue/cyan accents (no green primaries).
package tui

import "github.com/charmbracelet/lipgloss"

// ── Palette ───────────────────────────────────────────────────────────────

const (
	ColorPrimary  = lipgloss.Color("#5B8DFF") // primary accent (blue)
	ColorNeon     = lipgloss.Color("#7EC4FF") // bright highlight
	ColorBorder   = lipgloss.Color("#3A4254") // borders / chrome
	ColorSurface  = lipgloss.Color("#141821") // panel background
	ColorBg       = lipgloss.Color("#0B0D12") // near-black
	ColorMuted    = lipgloss.Color("#8B92A8") // secondary text
	ColorCyan     = lipgloss.Color("#5EC8E8") // tools / OK dot
	ColorError    = lipgloss.Color("#E07066") // errors
	ColorWhite    = lipgloss.Color("#E8EBF4") // primary text
	ColorUserMsg  = lipgloss.Color("#9BB8FF") // user emphasis
	ColorAgentMsg = lipgloss.Color("#7EC4FF") // agent emphasis
)

// ── Base Styles ───────────────────────────────────────────────────────────

var (
	// MainBorder is the outer rounded border used in panels.
	MainBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Background(ColorSurface).
			Padding(0, 1)

	// Header is the top title bar.
	Header = lipgloss.NewStyle().
		Foreground(ColorNeon).
		Bold(true)

	// Muted is used for secondary / hint text.
	Muted = lipgloss.NewStyle().Foreground(ColorMuted)

	// Primary is used for primary highlighted text.
	Primary = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	// Neon is the brightest pop.
	Neon = lipgloss.NewStyle().Foreground(ColorNeon).Bold(true)

	// UserPrefix styles the "You ›" label.
	UserPrefix = lipgloss.NewStyle().
			Foreground(ColorUserMsg).
			Bold(true)

	// AgentPrefix styles the "🤖" label.
	AgentPrefix = lipgloss.NewStyle().
			Foreground(ColorAgentMsg).
			Bold(true)

	// ToolBadge styles the [⚙ tool_name] indicator.
	ToolBadge = lipgloss.NewStyle().
			Foreground(ColorCyan).
			Bold(true)

	// ErrorStyle highlights error messages.
	ErrorStyle = lipgloss.NewStyle().Foreground(ColorError).Bold(true)

	// InputLine styles the textarea border when active.
	InputBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	// StatusOK is a cyan dot for running status (not green).
	StatusOK = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render("●")

	// StatusErr is a red dot for stopped status.
	StatusErr = lipgloss.NewStyle().Foreground(ColorError).Bold(true).Render("●")
)

// ── Helpers ───────────────────────────────────────────────────────────────

// Badge renders a small pill label.
func Badge(text string, active bool) string {
	color := ColorMuted
	if active {
		color = ColorPrimary
	}
	return lipgloss.NewStyle().
		Foreground(color).
		Bold(active).
		Render("  " + text)
}

// ProgressBar renders a simple ASCII progress bar.
func ProgressBar(percent float64, width int) string {
	filled := int(float64(width) * percent / 100)
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += lipgloss.NewStyle().Foreground(ColorCyan).Render("█")
		} else {
			bar += lipgloss.NewStyle().Foreground(ColorBorder).Render("░")
		}
	}
	return bar
}

// Divider renders a full-width separator line.
func Divider(width int) string {
	line := ""
	for i := 0; i < width; i++ {
		line += "─"
	}
	return Muted.Render(line)
}
