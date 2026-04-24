package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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
	search        textinput.Model
	searchActive  bool
}

// NewDashboardModel builds a dashboard. sessionUsage may be nil for standalone status.
func NewDashboardModel(info DashboardInfo, contextLabel string, sessionUsage *SessionUsage, overlay bool) *DashboardModel {
	ti := textinput.New()
	ti.Placeholder = "Search settings..."
	ti.CharLimit = 64
	ti.Width = 40
	ti.PromptStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	ti.TextStyle = lipgloss.NewStyle().Foreground(ColorWhite)
	return &DashboardModel{
		info:         info,
		contextLabel: strings.TrimSpace(contextLabel),
		sessionUsage: sessionUsage,
		overlay:      overlay,
		search:       ti,
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
		if m.searchActive {
			switch msg.String() {
			case "esc":
				m.searchActive = false
				m.search.Blur()
				m.search.SetValue("")
				return m, nil
			case "enter":
				m.searchActive = false
				m.search.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.search, cmd = m.search.Update(msg)
				return m, cmd
			}
		}
		switch msg.String() {
		case "ctrl+c", "q", "Q", "esc":
			if m.overlay {
				return m, nil
			}
			return m, tea.Quit
		case "/":
			m.searchActive = true
			m.search.Focus()
			return m, textinput.Blink
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

	// Search bar — shown between tabs and content
	searchBorderColor := ColorBorder
	if m.searchActive {
		searchBorderColor = ColorAccentLavender
	}
	searchBar := lipgloss.NewStyle().
		Width(w-4).
		Border(lipgloss.NormalBorder()).
		BorderForeground(searchBorderColor).
		Background(ColorDashboardBg).
		Padding(0, 1).
		MarginLeft(2).
		Render(m.search.View())

	divLine := strings.Repeat("─", max(0, w-2))
	div := lipgloss.NewStyle().Width(w).Padding(0, 1).Render(DashboardDivider.Render(divLine))

	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	body := m.renderTabBodyFiltered(query)
	body = clipDashboardLines(body, contentH-3) // -3 for search bar height

	panel := DashboardPanel.Width(w - 2).Render(body)

	footerParts := []string{"↑/↓/tab to switch", "/ to search", "Esc to close"}
	if !m.overlay {
		footerParts = []string{"↑/↓/tab to switch", "/ to search", "q / Esc to quit"}
	}
	footerText := strings.Join(footerParts, " · ")
	footer := lipgloss.NewStyle().Width(w).Background(ColorDashboardBg).Padding(0, 1).
		Foreground(ColorMuted).Render(footerText)

	return lipgloss.JoinVertical(lipgloss.Left, strip, tabRow, searchBar, div, panel, footer)
}

func (m *DashboardModel) renderTabBodyFiltered(query string) string {
	switch m.activeTab {
	case dashTabStatus:
		lines := StatusContentLines(m.info.Status, m.ps, true)
		title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Orchestrator")
		if query == "" {
			return title + "\n\n" + strings.Join(lines, "\n")
		}
		var filtered []string
		for _, l := range lines {
			if strings.Contains(strings.ToLower(l), query) {
				filtered = append(filtered, l)
			}
		}
		if len(filtered) == 0 {
			return title + "\n\n" + lipgloss.NewStyle().Foreground(ColorMuted).Render("No matches for "+strconv.Quote(query))
		}
		return title + "\n\n" + strings.Join(filtered, "\n")
	case dashTabConfig:
		return m.renderConfigTabFiltered(query)
	case dashTabLimits:
		return m.renderLimitsTabFiltered(query)
	case dashTabUsage:
		return m.renderUsageTabFiltered(query)
	default:
		return ""
	}
}

func (m *DashboardModel) renderConfigTabFiltered(query string) string {
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
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Configuration")
	var rows []string
	rows = append(rows, title, "")
	const keyW = 18
	matched := 0
	for _, row := range kv {
		if query != "" && !strings.Contains(row.k, query) && !strings.Contains(strings.ToLower(row.v), query) {
			continue
		}
		matched++
		pad := strings.Repeat(" ", max(0, keyW-len(row.k)))
		line := dim.Render(row.k+pad) + lav.Render(row.v)
		rows = append(rows, line)
	}
	if query != "" && matched == 0 {
		rows = append(rows, dim.Render("No matches for "+strconv.Quote(query)))
	}
	return strings.Join(rows, "\n")
}

func (m *DashboardModel) renderLimitsTabFiltered(query string) string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender)
	L := m.info.Limits

	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Limits")
	kv := []struct{ k, v string }{
		{"max_iterations", fmtLimit(L.MaxIterations)},
		{"working_token_budget", fmtLimit(L.WorkingTokenBudget)},
		{"mempalace.search_limit", fmtLimit(L.MemPalaceSearchLimit)},
		{"gateway.max_ws_clients", fmtLimit(L.MaxWebSocketClients)},
		{"subagent.max_concurrent", fmtLimit(L.SubagentMaxConcurrent)},
		{"subagent.retain_limit", fmtLimit(L.SubagentRetainLimit)},
		{"local_intel.max_tokens", fmtLimit(L.LocalIntelMaxTokens)},
		{"local_intel.smart_route_max", fmtLimit(L.SmartRoutingMaxTokens)},
	}
	rows := []string{title, ""}
	matched := 0
	for _, row := range kv {
		if query != "" && !strings.Contains(row.k, query) && !strings.Contains(row.v, query) {
			continue
		}
		matched++
		rows = append(rows, rowKV(dim, lav, row.k, row.v))
	}
	if query != "" && matched == 0 {
		rows = append(rows, dim.Render("No matches for "+strconv.Quote(query)))
	}
	return strings.Join(rows, "\n")
}

func (m *DashboardModel) renderUsageTabFiltered(query string) string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender)

	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Usage")
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")

	if m.sessionUsage != nil && m.sessionUsage.Turns > 0 {
		s := m.sessionUsage
		kv := []struct{ k, v string }{
			{"turns_completed", fmt.Sprintf("%d", s.Turns)},
			{"prompt_tokens", fmtNum(s.PromptTokens)},
			{"completion_tokens", fmtNum(s.CompletionTokens)},
			{"cache_read", fmtNum(s.CacheReadTokens)},
			{"cache_write", fmtNum(s.CacheWriteTokens)},
			{"total_tokens", fmtNum(s.TotalTokens)},
		}
		matched := 0
		for _, row := range kv {
			if query != "" && !strings.Contains(row.k, query) && !strings.Contains(row.v, query) {
				continue
			}
			matched++
			b.WriteString(rowKV(dim, lav, row.k, row.v))
			b.WriteString("\n")
		}
		if query != "" && matched == 0 {
			b.WriteString(dim.Render("No matches for " + strconv.Quote(query)))
		}
		return b.String()
	}

	b.WriteString(dim.Render("Session token usage appears after you send messages in the agent REPL."))
	b.WriteString("\n\n")
	ramMB := float64(m.ps.rssKB) / 1024.0
	if m.info.Status.PID > 0 && m.ps.alive {
		kv := []struct{ k, v string }{
			{"process_rss", fmt.Sprintf("%.1f MB", ramMB)},
			{"pid", fmt.Sprintf("%d", m.info.Status.PID)},
		}
		for _, row := range kv {
			if query != "" && !strings.Contains(row.k, query) && !strings.Contains(row.v, query) {
				continue
			}
			b.WriteString(rowKV(dim, lav, row.k, row.v))
			b.WriteString("\n")
		}
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
