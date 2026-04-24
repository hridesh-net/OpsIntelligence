package tui

// repos_tui.go — Repo Intelligence interactive TUI.
//
// Tabs: Repos | Users | Memory | Scans
// Layout mirrors the dashboard/onboard-summary style: dark chrome, lavender
// accents, live search, two-column key-value display.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/opsintelligence/opsintelligence/internal/repointel"
)

// ── Tab constants ─────────────────────────────────────────────────────────────

const (
	reposTabRepos  = 0
	reposTabUsers  = 1
	reposTabMemory = 2
	reposTabScans  = 3
	reposTabCount  = 4
)

var reposTabLabels = [reposTabCount]string{"Repos", "Users", "Memory", "Scans"}

// ── Model ─────────────────────────────────────────────────────────────────────

// ReposTUIModel is the Bubbletea model for the Repo Intelligence TUI.
type ReposTUIModel struct {
	registry *repointel.Registry
	entries  []repointel.RepoEntry

	activeTab    int
	selectedRepo int // index into entries for detail views

	search       textinput.Model
	searchActive bool

	width  int
	height int
}

// NewReposTUIModel constructs the model from a Registry.
func NewReposTUIModel(reg *repointel.Registry) ReposTUIModel {
	si := textinput.New()
	si.Placeholder = "Search repos..."
	si.CharLimit = 80

	entries := reg.List()

	return ReposTUIModel{
		registry: reg,
		entries:  entries,
		search:   si,
		width:    100,
		height:   30,
	}
}

// Init satisfies tea.Model.
func (m ReposTUIModel) Init() tea.Cmd {
	return nil
}

// Update satisfies tea.Model.
func (m ReposTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		// Route to search input first when active.
		if m.searchActive {
			switch msg.String() {
			case "esc", "enter":
				m.searchActive = false
				m.search.Blur()
			default:
				var cmd tea.Cmd
				m.search, cmd = m.search.Update(msg)
				return m, cmd
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.activeTab = (m.activeTab + 1) % reposTabCount
		case "shift+tab", "left", "h":
			m.activeTab = (m.activeTab - 1 + reposTabCount) % reposTabCount
		case "up", "k":
			if m.selectedRepo > 0 {
				m.selectedRepo--
			}
		case "down", "j":
			if m.selectedRepo < len(m.entries)-1 {
				m.selectedRepo++
			}
		case "/":
			m.searchActive = true
			m.search.Focus()
		case "r":
			// Refresh entries from registry.
			m.entries = m.registry.List()
		}
	}
	return m, nil
}

// View satisfies tea.Model.
func (m ReposTUIModel) View() string {
	query := strings.ToLower(m.search.Value())

	chromeBg := lipgloss.NewStyle().
		Background(ColorChromeBg).
		Foreground(ColorMuted).
		Width(m.width).
		Padding(0, 1)

	panelBg := lipgloss.NewStyle().
		Background(ColorDashboardBg).
		Width(m.width - 2).
		Padding(1, 2)

	// ── Context strip ────────────────────────────────────────────────────────
	contextStrip := chromeBg.Render(
		lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Repo Intelligence") +
			"  " + lipgloss.NewStyle().Foreground(ColorMuted).Render(fmt.Sprintf("%d repos configured", len(m.entries))),
	)

	// ── Tab pills ────────────────────────────────────────────────────────────
	var tabs []string
	for i, label := range reposTabLabels {
		if i == m.activeTab {
			tabs = append(tabs, lipgloss.NewStyle().
				Background(ColorAccentLavender).
				Foreground(ColorTabActiveFG).
				Bold(true).
				Padding(0, 2).
				Render(label))
		} else {
			tabs = append(tabs, lipgloss.NewStyle().
				Foreground(ColorMuted).
				Padding(0, 2).
				Render(label))
		}
	}
	tabRow := chromeBg.Render(strings.Join(tabs, " "))

	// ── Search bar ───────────────────────────────────────────────────────────
	searchBorderColor := ColorBorder
	if m.searchActive {
		searchBorderColor = ColorAccentLavender
	}
	searchBar := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(searchBorderColor).
		Background(ColorDashboardBg).
		Width(m.width - 6).
		Padding(0, 1).
		Render(m.search.View())

	// ── Divider ──────────────────────────────────────────────────────────────
	divider := lipgloss.NewStyle().
		Foreground(ColorBorder).
		Render(strings.Repeat("─", m.width-2))

	// ── Tab body ─────────────────────────────────────────────────────────────
	var body string
	switch m.activeTab {
	case reposTabRepos:
		body = m.renderReposTab(query)
	case reposTabUsers:
		body = m.renderUsersTab(query)
	case reposTabMemory:
		body = m.renderMemoryTab(query)
	case reposTabScans:
		body = m.renderScansTab(query)
	}

	// ── Footer ───────────────────────────────────────────────────────────────
	footer := chromeBg.Render(
		lipgloss.NewStyle().Foreground(ColorMuted).Render(
			"↑/↓ select · tab to switch · / to search · r to refresh · Esc to close",
		),
	)

	panel := panelBg.Render(body)

	return strings.Join([]string{
		contextStrip,
		tabRow,
		searchBar,
		divider,
		panel,
		footer,
	}, "\n")
}

// ── Tab renderers ─────────────────────────────────────────────────────────────

type repoKV struct{ k, v string }

func repoKVRow(k, v string) repoKV { return repoKV{k, v} }

func (m ReposTUIModel) renderKVRows(rows []repoKV, query string) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(ColorMuted).
		Width(22)
	valStyle := lipgloss.NewStyle().
		Foreground(ColorWhite)

	var lines []string
	for _, r := range rows {
		if query != "" && !strings.Contains(strings.ToLower(r.k), query) &&
			!strings.Contains(strings.ToLower(r.v), query) {
			continue
		}
		lines = append(lines, keyStyle.Render(r.k)+valStyle.Render(r.v))
	}
	return strings.Join(lines, "\n")
}

func riskColor(risk string) lipgloss.Style {
	switch risk {
	case "critical":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#E07066")).Bold(true)
	case "high":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F4A261"))
	case "medium":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#E9C46A"))
	case "low":
		return lipgloss.NewStyle().Foreground(ColorCyan)
	default:
		return lipgloss.NewStyle().Foreground(ColorMuted)
	}
}

func (m ReposTUIModel) filteredEntries(query string) []repointel.RepoEntry {
	if query == "" {
		return m.entries
	}
	var out []repointel.RepoEntry
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.FullName), query) ||
			strings.Contains(strings.ToLower(e.Language), query) {
			out = append(out, e)
		}
	}
	return out
}

// renderReposTab renders the Repos tab — a list of all repos with inline status.
func (m ReposTUIModel) renderReposTab(query string) string {
	entries := m.filteredEntries(query)
	if len(entries) == 0 {
		return lipgloss.NewStyle().Foreground(ColorMuted).Render("No repos match. Add one with: repos add <owner/name>")
	}

	header := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render(fmt.Sprintf("%-36s  %-10s  %-10s  %-8s  %s", "REPO", "INDEX", "SCAN", "RISK", "LANG"))
	divider := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", 80))

	rowStyle := lipgloss.NewStyle().Foreground(ColorWhite)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true)

	var lines []string
	lines = append(lines, header, divider)

	for i, e := range entries {
		risk := e.RiskLevel
		if risk == "" {
			risk = "—"
		}
		lang := e.Language
		if lang == "" {
			lang = "—"
		}
		row := fmt.Sprintf("%-36s  %-10s  %-10s  %-8s  %s",
			truncate36(e.FullName),
			string(e.IndexStatus),
			string(e.ScanStatus),
			risk,
			lang,
		)
		if i == m.selectedRepo {
			lines = append(lines, selectedStyle.Render("> "+row))
		} else {
			lines = append(lines, rowStyle.Render("  "+row))
		}
	}
	return strings.Join(lines, "\n")
}

// renderUsersTab renders the Users tab for the selected repo.
func (m ReposTUIModel) renderUsersTab(query string) string {
	if len(m.entries) == 0 {
		return lipgloss.NewStyle().Foreground(ColorMuted).Render("No repos configured.")
	}
	idx := m.selectedRepo
	if idx >= len(m.entries) {
		idx = 0
	}
	e := m.entries[idx]

	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Users — " + e.FullName)

	if len(e.Users) == 0 {
		return title + "\n\n" + lipgloss.NewStyle().Foreground(ColorMuted).
			Render("No users configured. Use: repos users add <owner/name> <handle>")
	}

	var rows []repoKV
	for _, u := range e.Users {
		v := string(u.Role)
		if u.Email != "" {
			v += "  <" + u.Email + ">"
		}
		rows = append(rows, repoKVRow(u.Handle, v))
	}

	return title + "\n\n" + m.renderKVRows(rows, query)
}

// renderMemoryTab renders the Memory tab for the selected repo.
func (m ReposTUIModel) renderMemoryTab(query string) string {
	if len(m.entries) == 0 {
		return lipgloss.NewStyle().Foreground(ColorMuted).Render("No repos configured.")
	}
	idx := m.selectedRepo
	if idx >= len(m.entries) {
		idx = 0
	}
	e := m.entries[idx]

	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Memory — " + e.FullName)

	if e.IndexStatus != repointel.IndexReady {
		return title + "\n\n" +
			lipgloss.NewStyle().Foreground(ColorMuted).Render("Not yet indexed. Status: "+string(e.IndexStatus))
	}

	rows := []repoKV{
		repoKVRow("Index status", string(e.IndexStatus)),
		repoKVRow("Indexed at", fmtTime(e.IndexedAt)),
		repoKVRow("Head SHA", shorten(e.HeadSHA, 12)),
		repoKVRow("Language", e.Language),
		repoKVRow("Memory file", e.MemoryFile),
	}
	return title + "\n\n" + m.renderKVRows(rows, query)
}

// renderScansTab renders the Scans tab for the selected repo.
func (m ReposTUIModel) renderScansTab(query string) string {
	if len(m.entries) == 0 {
		return lipgloss.NewStyle().Foreground(ColorMuted).Render("No repos configured.")
	}
	idx := m.selectedRepo
	if idx >= len(m.entries) {
		idx = 0
	}
	e := m.entries[idx]

	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Scans — " + e.FullName)

	if e.ScanStatus != repointel.ScanDone {
		return title + "\n\n" +
			lipgloss.NewStyle().Foreground(ColorMuted).Render("Not yet scanned. Status: "+string(e.ScanStatus))
	}

	riskLabel := e.RiskLevel
	if riskLabel == "" {
		riskLabel = "info"
	}
	riskStyled := riskColor(riskLabel).Render(strings.ToUpper(riskLabel))

	rows := []repoKV{
		repoKVRow("Scan status", string(e.ScanStatus)),
		repoKVRow("Scanned at", fmtTime(e.ScannedAt)),
		repoKVRow("Risk level", riskStyled),
		repoKVRow("Scan file", e.ScanFile),
	}

	if query != "" {
		return title + "\n\n" + m.renderKVRows(rows, query)
	}
	return title + "\n\n" + m.renderKVRows(rows, "")
}

// ── Entry point ───────────────────────────────────────────────────────────────

// ReposTUIRun is called by repos_cmd.go — exported so the package can use it.
// It's referenced as `reposTUIRun` (lowercase) from `repos_cmd.go` which is in
// `package main`, so we expose a thin shim below.
func ReposTUIRun(reg *repointel.Registry) error {
	m := NewReposTUIModel(reg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ── Small helpers ─────────────────────────────────────────────────────────────

func truncate36(s string) string {
	if len(s) <= 36 {
		return s
	}
	return s[:33] + "..."
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04 UTC")
}
