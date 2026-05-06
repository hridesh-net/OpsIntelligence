package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// TokenUsageSnapshot mirrors provider.TokenUsage without importing provider here.
type TokenUsageSnapshot struct {
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
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

	// Live data — read on every render, not a snapshot.
	Tasks        *subagents.TaskManager // for Agents tab
	RunTracePath string                 // NDJSON path for Logs tab
	DatastoreKind string                // "sqlite" or "postgres" for Limits tab
}

// SessionUsage accumulates REPL session token usage for the Usage tab.
type SessionUsage struct {
	Turns            int
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
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
	dashTabAgents
	dashTabLogs
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

	// Log cache — populated incrementally on each tick, never on render.
	// Only new bytes since logOffset are read; the entry slice is capped at 200.
	logCache     []logEntry
	logOffset    int64
	logCachePath string // detects if RunTracePath changes between ticks

	// visible is true when this model is actually being shown in the REPL overlay.
	// fetchPS is only spawned when visible; the log cache refreshes regardless.
	visible bool
}

// SetVisible tells the dashboard whether it is currently shown to the user.
// Call with true on Ctrl+O open, false on close. Avoids spawning the ps
// subprocess every second when the dashboard is not on screen.
func (m *DashboardModel) SetVisible(v bool) { m.visible = v }

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
		m.refreshLogCache() // always: cheap incremental read, keeps cache warm
		pid := m.info.Status.PID
		// fetchPS spawns an OS subprocess — only worth doing when the dashboard
		// is actually visible (!overlay = standalone status command; visible = REPL overlay open).
		if pid > 0 && (!m.overlay || m.visible) {
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

	tabNames := []string{"Status", "Config", "Limits", "Usage", "Agents", "Logs"}
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
	case dashTabAgents:
		return m.renderAgentsTab(query)
	case dashTabLogs:
		return m.renderLogsTab(query)
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

	ds := nz(m.info.DatastoreKind, "sqlite")
	maxSessions := "~50–100  (SQLite WAL, single-writer)"
	if ds == "postgres" {
		maxSessions = "~500+  (tune max_open_conns)"
	}

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
		{"session_store", "in-process RAM"},
		{"datastore", ds},
		{"horizontal_scaling", "✗  single-instance"},
		{"recommended_max_sessions", maxSessions},
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

// ── Log cache — incremental reader ───────────────────────────────────────────

const logCacheMax = 200

// refreshLogCache reads only the new bytes appended to the run trace since the
// last call. It is driven by the dashboard tick (~1s) so rendering is never
// blocked by file I/O. The in-memory cache never exceeds logCacheMax entries.
func (m *DashboardModel) refreshLogCache() {
	path := m.info.RunTracePath
	if path == "" {
		return
	}
	// Reset if the trace path changed (e.g. new session).
	if path != m.logCachePath {
		m.logCachePath = path
		m.logOffset = 0
		m.logCache = m.logCache[:0]
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.Size() <= m.logOffset {
		return // nothing new
	}

	if _, err := f.Seek(m.logOffset, io.SeekStart); err != nil {
		return
	}

	sc := bufio.NewScanner(f)
	var newEntries []logEntry
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e logEntry
		if json.Unmarshal(line, &e) == nil {
			newEntries = append(newEntries, e)
		}
	}

	// Record how far we got so the next tick only reads what's new.
	if pos, err := f.Seek(0, io.SeekCurrent); err == nil {
		m.logOffset = pos
	}

	if len(newEntries) == 0 {
		return
	}

	m.logCache = append(m.logCache, newEntries...)
	// Trim to cap without allocating a new backing array.
	if len(m.logCache) > logCacheMax {
		copy(m.logCache, m.logCache[len(m.logCache)-logCacheMax:])
		m.logCache = m.logCache[:logCacheMax]
	}
}

// ── Agents tab ────────────────────────────────────────────────────────────────

func (m *DashboardModel) renderAgentsTab(query string) string {
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Active Agents")
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	green := lipgloss.NewStyle().Foreground(ColorSuccess)
	orange := lipgloss.NewStyle().Foreground(ColorNeon)
	errSt := lipgloss.NewStyle().Foreground(ColorError)

	if m.info.Tasks == nil {
		return title + "\n\n" + dim.Render("Task manager not available.")
	}
	tasks := m.info.Tasks.List()

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n\n")

	// Master row is always present.
	sb.WriteString(green.Render("■ master") + "  " + dim.Render("running") + "\n")

	active := 0
	for _, tk := range tasks {
		if tk.Status != subagents.StatusRunning && tk.Status != subagents.StatusPending {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(tk.SubAgentNm), query) &&
			!strings.Contains(strings.ToLower(string(tk.Status)), query) {
			continue
		}
		active++
		icon := green.Render("■")
		statusStr := green.Render(string(tk.Status))
		if tk.Status == subagents.StatusPending {
			icon = orange.Render("◷")
			statusStr = orange.Render(string(tk.Status))
		}
		elapsed := tk.Elapsed().Round(time.Second)
		sb.WriteString(fmt.Sprintf("  └─ %s %-12s %s  %s\n",
			icon,
			lipgloss.NewStyle().Bold(true).Render(tk.SubAgentNm),
			statusStr,
			dim.Render(elapsed.String()),
		))
		last := tk.LastEvent()
		if last.Message != "" {
			phase := last.Phase
			if phase == "" {
				phase = string(last.Kind)
			}
			msg := last.Message
			if len([]rune(msg)) > 72 {
				msg = string([]rune(msg)[:72]) + "…"
			}
			sb.WriteString(fmt.Sprintf("       %s %s\n",
				dim.Render("["+phase+"]"),
				dim.Render(msg),
			))
		}
	}

	if active == 0 && query == "" {
		sb.WriteString("  " + dim.Render("(no active specialists)") + "\n")
	}

	// Completed / failed agents
	var done []subagents.Task
	for _, tk := range tasks {
		if tk.Status == subagents.StatusRunning || tk.Status == subagents.StatusPending {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(tk.SubAgentNm), query) &&
			!strings.Contains(strings.ToLower(string(tk.Status)), query) {
			continue
		}
		done = append(done, tk)
	}
	if len(done) > 0 {
		sb.WriteString("\n" + dim.Render("── Completed ─────────────────────────────────") + "\n")
		for _, tk := range done {
			icon := green.Render("✓")
			if tk.Status == subagents.StatusFailed {
				icon = errSt.Render("✗")
			} else if tk.Status == subagents.StatusCancelled {
				icon = dim.Render("⊘")
			}
			elapsed := tk.Elapsed().Round(time.Second)
			extra := ""
			if tk.Error != "" {
				msg := tk.Error
				if len([]rune(msg)) > 50 {
					msg = string([]rune(msg)[:50]) + "…"
				}
				extra = "  " + errSt.Render(msg)
			}
			sb.WriteString(fmt.Sprintf("%s %-14s %s  %s%s\n",
				icon,
				lipgloss.NewStyle().Bold(true).Render(tk.SubAgentNm),
				dim.Render(string(tk.Status)),
				dim.Render(elapsed.String()),
				extra,
			))
		}
	}

	if active == 0 && len(done) == 0 {
		sb.WriteString("\n" + dim.Render("No specialist agents have been spawned yet in this session."))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ── Logs tab ──────────────────────────────────────────────────────────────────

// logEntry is one parsed line from the run trace NDJSON.
type logEntry struct {
	T            string `json:"t"`
	Kind         string `json:"kind"`
	RunnerRole   string `json:"runner_role"`
	SessionID    string `json:"session_id"`
	ParentAgent  string `json:"parent_agent_id"`
	Tool         string `json:"tool"`
	Result       string `json:"result"`
	Iteration    int    `json:"iteration"`
	QueryPreview string `json:"query_preview"`
	Error        string `json:"error"`
	Finish       string `json:"finish_reason"`
}

// renderLogsTab renders the Logs tab purely from the in-memory cache — zero
// file I/O. The cache is refreshed incrementally on every dashboard tick.
func (m *DashboardModel) renderLogsTab(query string) string {
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Run Trace")
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	cyan := lipgloss.NewStyle().Foreground(ColorCyan)
	green := lipgloss.NewStyle().Foreground(ColorSuccess)
	orange := lipgloss.NewStyle().Foreground(ColorNeon)
	errSt := lipgloss.NewStyle().Foreground(ColorError)
	white := lipgloss.NewStyle().Foreground(ColorWhite)

	path := m.info.RunTracePath
	if path == "" {
		return title + "\n\n" + dim.Render("Run trace not configured.")
	}

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("  ")
	sb.WriteString(dim.Render(path))
	sb.WriteString("\n\n")

	// Header row
	sb.WriteString(dim.Render(fmt.Sprintf("%-8s  %-8s  %-18s  %s\n", "TIME", "ROLE", "EVENT", "DETAIL")))
	sb.WriteString(dim.Render(strings.Repeat("─", 70)) + "\n")

	entries := m.logCache
	if len(entries) == 0 {
		sb.WriteString(dim.Render("No trace events yet. Send a message to the agent to start."))
		return sb.String()
	}

	matched := 0
	for _, e := range entries {

		// Build detail string
		var detail string
		switch e.Kind {
		case "task_start":
			detail = e.QueryPreview
			if e.ParentAgent != "" {
				detail += dim.Render("  ← "+e.ParentAgent[:min8(e.ParentAgent)])
			}
		case "model_iteration":
			detail = fmt.Sprintf("iter=%d", e.Iteration)
		case "tool_call", "tool_result", "tool_done":
			detail = e.Tool
			if e.Result != "" {
				r := e.Result
				if len([]rune(r)) > 50 {
					r = string([]rune(r)[:50]) + "…"
				}
				detail += "  → " + r
			}
		case "task_done":
			detail = "finish=" + e.Finish
			if e.Error != "" {
				detail += "  err=" + e.Error
			}
		default:
			detail = e.Kind
		}

		// Filter
		searchTarget := strings.ToLower(e.Kind + " " + e.RunnerRole + " " + detail + " " + e.Tool)
		if query != "" && !strings.Contains(searchTarget, query) {
			continue
		}
		matched++

		// Timestamp — show only HH:MM:SS
		ts := e.T
		if len(ts) >= 19 {
			ts = ts[11:19]
		}

		// Role badge
		roleBadge := cyan.Render("master  ")
		if e.RunnerRole == "subagent" {
			roleBadge = orange.Render("agent   ")
		}

		// Kind color
		kindStr := white.Render(e.Kind)
		switch e.Kind {
		case "task_start":
			kindStr = green.Render(fmt.Sprintf("%-18s", e.Kind))
		case "task_done":
			if e.Error != "" {
				kindStr = errSt.Render(fmt.Sprintf("%-18s", e.Kind))
			} else {
				kindStr = green.Render(fmt.Sprintf("%-18s", e.Kind))
			}
		case "tool_call":
			kindStr = cyan.Render(fmt.Sprintf("%-18s", e.Kind))
		case "tool_done", "tool_result":
			kindStr = dim.Render(fmt.Sprintf("%-18s", e.Kind))
		default:
			kindStr = white.Render(fmt.Sprintf("%-18s", e.Kind))
		}

		// Truncate detail
		detailRunes := []rune(detail)
		if len(detailRunes) > 50 {
			detail = string(detailRunes[:50]) + "…"
		}

		sb.WriteString(fmt.Sprintf("%s  %s  %s  %s\n",
			dim.Render(ts), roleBadge, kindStr, dim.Render(detail)))
	}

	if matched == 0 && query != "" {
		sb.WriteString(dim.Render("No entries matching " + strconv.Quote(query)))
	}

	return strings.TrimRight(sb.String(), "\n")
}

func min8(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
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
