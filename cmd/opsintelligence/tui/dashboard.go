package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TokenUsageSnapshot mirrors provider.TokenUsage without importing provider here.
type TokenUsageSnapshot struct {
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens    int
	CacheWriteTokens   int
	TotalTokens        int
}

// LimitsSnapshot holds numeric limits for the Limits tab.
type LimitsSnapshot struct {
	MaxIterations         int
	WorkingTokenBudget    int
	MemPalaceSearchLimit  int
	MaxWebSocketClients   int
	SubagentMaxConcurrent int
	SubagentRetainLimit   int
	LocalIntelMaxTokens   int
	SmartRoutingMaxTokens int
}

// DashboardInfo is a read-only snapshot for the Config / Limits tabs plus StatusInfo.
type DashboardInfo struct {
	Status StatusInfo

	ConfigPath        string
	StateDir          string
	CWD               string
	RoutingModel      string
	GatewayHostPort   string
	GatewayPublicBase string
	Enterprise        bool
	Planning          string
	Reflection        string
	MemPalaceEnabled  bool
	LocalIntelEnabled bool
	MCPClientCount    int

	Limits LimitsSnapshot
}

// SessionUsage accumulates REPL session token usage for the Usage tab.
type SessionUsage struct {
	Turns              int
	PromptTokens       int
	CompletionTokens   int
	CacheReadTokens    int
	CacheWriteTokens   int
	TotalTokens        int
}

// Add merges one completion into the session totals.
func (s *SessionUsage) Add(u TokenUsageSnapshot) {
	s.Turns++
	s.PromptTokens += u.PromptTokens
	s.CompletionTokens += u.CompletionTokens
	s.CacheReadTokens += u.CacheReadTokens
	s.CacheWriteTokens += u.CacheWriteTokens
	s.TotalTokens += u.TotalTokens
}

const (
	dashTabStatus = iota
	dashTabConfig
	dashTabLimits
	dashTabUsage
	dashTabCount
)

// DashboardModel is the Claude-style tabbed status / config / limits / usage UI.
type DashboardModel struct {
	info          DashboardInfo
	contextLabel  string
	activeTab     int
	width, height int
	ps            psResult
	sessionUsage  *SessionUsage
	overlay       bool // when true, Esc/q do not quit (parent REPL handles dismiss)
}

// NewDashboardModel builds a dashboard. sessionUsage may be nil for standalone status.
func NewDashboardModel(info DashboardInfo, contextLabel string, sessionUsage *SessionUsage, overlay bool) *DashboardModel {
	return &DashboardModel{
		info:         info,
		contextLabel: strings.TrimSpace(contextLabel),
		sessionUsage: sessionUsage,
		overlay:      overlay,
	}
}

func (m *DashboardModel) Init() tea.Cmd {
	pid := m.info.Status.PID
	if pid <= 0 {
		return tickEvery()
	}
	return tea.Batch(fetchPS(pid), tickEvery())
}

func (m *DashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "Q", "esc":
			if m.overlay {
				return m, nil
			}
			return m, tea.Quit
		case "left":
			m.activeTab = (m.activeTab - 1 + dashTabCount) % dashTabCount
		case "right", "tab":
			m.activeTab = (m.activeTab + 1) % dashTabCount
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + dashTabCount) % dashTabCount
		}

	case tickMsg:
		pid := m.info.Status.PID
		if pid > 0 {
			return m, tea.Batch(fetchPS(pid), tickEvery())
		}
		return m, tickEvery()

	case psResult:
		m.ps = msg
	}
	return m, nil
}

func (m *DashboardModel) View() string {
	if m.width <= 0 {
		m.width = 80
	}
	w := m.width
	chromeH := 5
	contentH := m.height - chromeH
	if contentH < 6 {
		contentH = 6
	}

	strip := lipgloss.NewStyle().Width(w).Background(ColorChromeBg).Padding(0, 1).Render(
		ChromePrompt.Render("›") + " " + lipgloss.NewStyle().Background(ColorChromeBg).Foreground(ColorWhite).Render(m.contextLabel),
	)

	tabNames := []string{"Status", "Config", "Limits", "Usage"}
	var tabParts []string
	for i, name := range tabNames {
		if i == m.activeTab {
			tabParts = append(tabParts, TabActive.Render(name))
		} else {
			tabParts = append(tabParts, TabInactive.Render(name))
		}
	}
	tabRow := lipgloss.NewStyle().Width(w).Background(ColorDashboardBg).Padding(0, 1).
		Render(strings.Join(tabParts, "  "))

	divLine := strings.Repeat("─", max(0, w-2))
	div := lipgloss.NewStyle().Width(w).Padding(0, 1).Render(DashboardDivider.Render(divLine))

	body := m.renderTabBody()
	body = clipDashboardLines(body, contentH)

	panel := DashboardPanel.Width(w - 2).Render(body)

	footerText := "←/→ · tab · shift+tab · q / esc quit"
	if m.overlay {
		footerText = "←/→ · tab · esc / ctrl+o close"
	}
	footer := lipgloss.NewStyle().Width(w).Background(ColorDashboardBg).Padding(0, 1).
		Foreground(ColorMuted).Render(footerText)

	return lipgloss.JoinVertical(lipgloss.Left, strip, tabRow, div, panel, footer)
}

func (m *DashboardModel) renderTabBody() string {
	switch m.activeTab {
	case dashTabStatus:
		lines := StatusContentLines(m.info.Status, m.ps, true)
		title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Orchestrator")
		return title + "\n\n" + strings.Join(lines, "\n")
	case dashTabConfig:
		return m.renderConfigTab()
	case dashTabLimits:
		return m.renderLimitsTab()
	case dashTabUsage:
		return m.renderUsageTab()
	default:
		return ""
	}
}

func (m *DashboardModel) renderConfigTab() string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender)

	ent := "false"
	if m.info.Enterprise {
		ent = "true"
	}
	mp := "false"
	if m.info.MemPalaceEnabled {
		mp = "true"
	}
	li := "false"
	if m.info.LocalIntelEnabled {
		li = "true"
	}
	kv := []struct{ k, v string }{
		{"config file", nz(m.info.ConfigPath, "—")},
		{"state_dir", nz(m.info.StateDir, "—")},
		{"cwd", nz(m.info.CWD, "—")},
		{"routing.default", nz(m.info.RoutingModel, "—")},
		{"gateway.listen", nz(m.info.GatewayHostPort, "—")},
		{"gateway.public", nz(m.info.GatewayPublicBase, "—")},
		{"enterprise", ent},
		{"planning", nz(m.info.Planning, "—")},
		{"reflection", nz(m.info.Reflection, "—")},
		{"mempalace", mp},
		{"local_intel", li},
		{"mcp.clients", fmt.Sprintf("%d", m.info.MCPClientCount)},
	}
	var rows []string
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Configuration")
	rows = append(rows, title, "")
	const keyW = 18
	for _, row := range kv {
		pad := strings.Repeat(" ", max(0, keyW-len(row.k)))
		line := dim.Render(row.k+pad) + lav.Render(row.v)
		rows = append(rows, line)
	}
	return strings.Join(rows, "\n")
}

func (m *DashboardModel) renderLimitsTab() string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender)
	L := m.info.Limits

	rows := []string{
		lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Limits"),
		"",
		rowKV(dim, lav, "max_iterations", fmtLimit(L.MaxIterations)),
		rowKV(dim, lav, "working_token_budget", fmtLimit(L.WorkingTokenBudget)),
		rowKV(dim, lav, "mempalace.search_limit", fmtLimit(L.MemPalaceSearchLimit)),
		rowKV(dim, lav, "gateway.max_ws_clients", fmtLimit(L.MaxWebSocketClients)),
		rowKV(dim, lav, "subagent.max_concurrent", fmtLimit(L.SubagentMaxConcurrent)),
		rowKV(dim, lav, "subagent.retain_limit", fmtLimit(L.SubagentRetainLimit)),
		rowKV(dim, lav, "local_intel.max_tokens", fmtLimit(L.LocalIntelMaxTokens)),
		rowKV(dim, lav, "local_intel.smart_route_max", fmtLimit(L.SmartRoutingMaxTokens)),
	}
	return strings.Join(rows, "\n")
}

func (m *DashboardModel) renderUsageTab() string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender)

	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Usage")
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")

	if m.sessionUsage != nil && m.sessionUsage.Turns > 0 {
		s := m.sessionUsage
		b.WriteString(rowKV(dim, lav, "turns_completed", fmt.Sprintf("%d", s.Turns)))
		b.WriteString("\n")
		b.WriteString(rowKV(dim, lav, "prompt_tokens", fmtNum(s.PromptTokens)))
		b.WriteString("\n")
		b.WriteString(rowKV(dim, lav, "completion_tokens", fmtNum(s.CompletionTokens)))
		b.WriteString("\n")
		b.WriteString(rowKV(dim, lav, "cache_read", fmtNum(s.CacheReadTokens)))
		b.WriteString("\n")
		b.WriteString(rowKV(dim, lav, "cache_write", fmtNum(s.CacheWriteTokens)))
		b.WriteString("\n")
		b.WriteString(rowKV(dim, lav, "total_tokens", fmtNum(s.TotalTokens)))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(dim.Render("Session token usage appears after you send messages in the agent REPL."))
	b.WriteString("\n\n")
	ramMB := float64(m.ps.rssKB) / 1024.0
	if m.info.Status.PID > 0 && m.ps.alive {
		b.WriteString(rowKV(dim, lav, "process_rss", fmt.Sprintf("%.1f MB", ramMB)))
		b.WriteString("\n")
		b.WriteString(rowKV(dim, lav, "pid", fmt.Sprintf("%d", m.info.Status.PID)))
		b.WriteString("\n")
	} else if m.info.Status.PID > 0 {
		b.WriteString(dim.Render("Process not running or PID unavailable for RSS."))
	} else {
		b.WriteString(dim.Render("No PID context (start the agent REPL for live session usage)."))
	}
	return b.String()
}

func rowKV(dim, valStyle lipgloss.Style, k, v string) string {
	const keyW = 22
	pad := strings.Repeat(" ", max(0, keyW-len(k)))
	return dim.Render(k+pad) + valStyle.Render(v)
}

func nz(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func fmtLimit(n int) string {
	return fmt.Sprintf("%d", n)
}

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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RunDashboard starts the tabbed dashboard full screen.
func RunDashboard(info DashboardInfo, contextLabel string, sessionUsage *SessionUsage, overlay bool) error {
	m := NewDashboardModel(info, contextLabel, sessionUsage, overlay)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
