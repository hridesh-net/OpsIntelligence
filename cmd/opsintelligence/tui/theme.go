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

	// Claude-style dashboard chrome (charcoal + lavender tabs).
	ColorChromeBg     = lipgloss.Color("#252733") // context strip / chrome
	ColorDashboardBg  = lipgloss.Color("#2b2d3c") // main panel fill
	ColorAccentLavender = lipgloss.Color("#b4befe") // active tab, accents
	ColorTabActiveFG  = lipgloss.Color("#1e1f2e") // text on lavender pill
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

	// ToolBadge styles tool name labels in the REPL.
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

	// ChromeStrip is the top context line (e.g. › /config).
	ChromeStrip = lipgloss.NewStyle().
			Background(ColorChromeBg).
			Foreground(ColorWhite).
			Padding(0, 1)

	ChromePrompt = lipgloss.NewStyle().
			Background(ColorChromeBg).
			Foreground(ColorMuted)

	// TabActive / TabInactive style the dashboard tab row.
	TabActive = lipgloss.NewStyle().
			Background(ColorAccentLavender).
			Foreground(ColorTabActiveFG).
			Bold(true).
			Padding(0, 1).
			MarginRight(1)

	TabInactive = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(0, 1).
			MarginRight(1)

	DashboardFooter = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(0, 1)

	DashboardPanel = lipgloss.NewStyle().
			Background(ColorDashboardBg).
			Foreground(ColorWhite).
			Padding(1, 2)

	DashboardDivider = lipgloss.NewStyle().
			Foreground(ColorAccentLavender)
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

// ProgressBarLavender is like ProgressBar but uses the dashboard accent fill.
func ProgressBarLavender(percent float64, width int) string {
	filled := int(float64(width) * percent / 100)
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += lipgloss.NewStyle().Foreground(ColorAccentLavender).Render("█")
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
