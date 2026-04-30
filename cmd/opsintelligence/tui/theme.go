// Package tui provides the OpsIntelligence terminal UI components.
// Palette: High-End Tech / AssistClaw — cream neutrals, charcoal structure,
// Modern Orange accent (#FF7043). Colors use lipgloss.AdaptiveColor so the
// same binary fits light terminals (web-aligned) and dark terminals (warm shell).
//
// Web typography (Plus Jakarta Sans, Inter, 8px grid) is not controllable here;
// only color and weight map to the terminal.
package tui

import "github.com/charmbracelet/lipgloss"

// ── Semantic palette (adaptive) ───────────────────────────────────────────

var (
	// App canvas
	ColorBackground = lipgloss.AdaptiveColor{Light: "#faf9f7", Dark: "#141411"}

	// Panels and elevated surfaces
	ColorSurface      = lipgloss.AdaptiveColor{Light: "#eeeeec", Dark: "#1e1f1c"}
	ColorChromeBg     = lipgloss.AdaptiveColor{Light: "#e8e8e6", Dark: "#2a2b28"}
	ColorDashboardBg  = lipgloss.AdaptiveColor{Light: "#f4f4f1", Dark: "#252620"}
	ColorBracketMuted = lipgloss.AdaptiveColor{Light: "#474646", Dark: "#8a8680"} // brackets, subtle chrome

	// Text
	ColorOnSurface = lipgloss.AdaptiveColor{Light: "#1a1c1b", Dark: "#ede9e4"}
	ColorMuted     = lipgloss.AdaptiveColor{Light: "#444748", Dark: "#8a8680"}
	ColorEmphasis  = lipgloss.AdaptiveColor{Light: "#000000", Dark: "#f1f1ef"} // headlines / strong structure

	// Lines and borders (outline / outline-variant from tokens)
	ColorOutline        = lipgloss.AdaptiveColor{Light: "#747878", Dark: "#5d5f5d"}
	ColorOutlineVariant = lipgloss.AdaptiveColor{Light: "#c4c7c7", Dark: "#3a3b38"}

	// Brand accent — Modern Orange (same in both modes for recognition)
	ColorBrandAccent     = lipgloss.AdaptiveColor{Light: "#FF7043", Dark: "#FF7043"}
	ColorBrandAccentSoft = lipgloss.AdaptiveColor{Light: "#FF9575", Dark: "#FF9575"}

	// Secondary structure (M3 secondary)
	ColorSecondary = lipgloss.AdaptiveColor{Light: "#5d5f5d", Dark: "#9aad9e"}

	// Status
	ColorSuccess = lipgloss.AdaptiveColor{Light: "#2e6b52", Dark: "#4CAF7D"}
	ColorError   = lipgloss.AdaptiveColor{Light: "#ba1a1a", Dark: "#ba1a1a"}

	// Text on orange (tabs, pills)
	ColorOnAccent = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#ffffff"}

	// CVE / risk emphasis (readable on cream and on dark)
	ColorRiskCritical = lipgloss.AdaptiveColor{Light: "#93000a", Dark: "#E07066"}
	ColorRiskHigh     = lipgloss.AdaptiveColor{Light: "#b85c00", Dark: "#F4A261"}
	ColorRiskMedium   = lipgloss.AdaptiveColor{Light: "#7d6510", Dark: "#E9C46A"}

	// “All green” / fully synced progress (GitHub-adjacent but adapted)
	ColorPatchOK = lipgloss.AdaptiveColor{Light: "#2e7d40", Dark: "#3fb950"}

	// Doctor / general warn (amber)
	ColorWarn = lipgloss.AdaptiveColor{Light: "#7d5f00", Dark: "#e6b800"}
)

// ── Legacy names (same values; keep call sites stable) ─────────────────────

var (
	ColorPrimary        = ColorBrandAccent     // orange CTA / selection / progress fill
	ColorNeon           = ColorBrandAccentSoft // soft orange highlight
	ColorBorder         = ColorOutlineVariant  // default hairline borders
	ColorBg             = ColorBackground
	ColorWhite          = ColorOnSurface // legacy: was “cream text”; now primary foreground
	ColorCyan           = ColorSuccess   // legacy: success / spinners (not cyan)
	ColorUserMsg        = ColorBrandAccent
	ColorAgentMsg       = ColorBrandAccentSoft
	ColorAccentLavender = ColorBrandAccent // legacy: tab / section accent
	ColorTabActiveFG    = ColorOnAccent
)

// ── Base Styles ───────────────────────────────────────────────────────────

var (
	// MainBorder is the outer rounded border used in panels.
	MainBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorOutline).
			Background(ColorSurface).
			Padding(0, 1)

	// Header is the top title bar (charcoal / cream emphasis, not orange).
	Header = lipgloss.NewStyle().
		Foreground(ColorEmphasis).
		Bold(true)

	// Muted is used for secondary / hint text.
	Muted = lipgloss.NewStyle().Foreground(ColorMuted)

	// Primary is used for primary highlighted text (brand orange).
	Primary = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	// Neon is the brightest accent pop.
	Neon = lipgloss.NewStyle().Foreground(ColorNeon).Bold(true)

	// ToolBadge styles tool name labels in the REPL.
	ToolBadge = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Bold(true)

	// ErrorStyle highlights error messages.
	ErrorStyle = lipgloss.NewStyle().Foreground(ColorError).Bold(true)

	// InputBorder styles the textarea border when active.
	InputBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(ColorOutline).
			Padding(0, 1)

	// StatusOK is a success-colored dot for running status.
	StatusOK = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("●")

	// StatusErr is a red dot for stopped status.
	StatusErr = lipgloss.NewStyle().Foreground(ColorError).Bold(true).Render("●")

	// ChromeStrip is the top context line (e.g. › /config).
	ChromeStrip = lipgloss.NewStyle().
			Background(ColorChromeBg).
			Foreground(ColorOnSurface).
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
			Foreground(ColorOnSurface).
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

// ProgressBar renders a simple ASCII progress bar (success-toned fill).
func ProgressBar(percent float64, width int) string {
	filled := int(float64(width) * percent / 100)
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += lipgloss.NewStyle().Foreground(ColorSuccess).Render("█")
		} else {
			bar += lipgloss.NewStyle().Foreground(ColorBorder).Render("░")
		}
	}
	return bar
}

// ProgressBarLavender renders a progress bar using the brand orange accent.
func ProgressBarLavender(percent float64, width int) string {
	filled := int(float64(width) * percent / 100)
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += lipgloss.NewStyle().Foreground(ColorPrimary).Render("█")
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
