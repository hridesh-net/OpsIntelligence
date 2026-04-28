package tui

// repos_tui.go — Repo Intelligence monitoring dashboard.
//
// Tabs: Repos | Memory | Scans | Users
//
//   Repos  — live list with index/scan progress, auto-refreshes every 3 s
//   Memory — full RepoMemory content (architecture, conventions, CVEs…)
//             press e to open edit form; operator notes persist to JSON
//   Scans  — full ScanResult (risk, summary, CVEs, bottlenecks, suggestions)
//   Users  — per-repo user list
//
// s = queue re-sync for the selected repo
// e = open edit form (Memory tab only)
// r = manual refresh

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/opsintelligence/opsintelligence/internal/repointel"
)

// ── Tab constants ─────────────────────────────────────────────────────────────

const (
	reposTabRepos  = 0
	reposTabMemory = 1
	reposTabScans  = 2
	reposTabUsers  = 3
	reposTabGraph  = 4
	reposTabCount  = 5
)

var reposTabLabels = [reposTabCount]string{"Repos", "Memory", "Scans", "Users", "Graph"}

// ── Tea messages ──────────────────────────────────────────────────────────────

type repoAutoRefreshMsg struct{}
type repoSaveDoneMsg struct{ err error }
type repoGraphExportDoneMsg struct{ path string; err error }
type repoProgressMsg struct{ ev repointel.ProgressEvent }

func repoRefreshCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return repoAutoRefreshMsg{} })
}

// ── Mode ─────────────────────────────────────────────────────────────────────

type repoTUIMode int

const (
	modeBrowse repoTUIMode = iota
	modeEdit
)

// ── Config / entry point ──────────────────────────────────────────────────────

// ReposTUIConfig bundles what the TUI needs (both from CLI and agent contexts).
type ReposTUIConfig struct {
	Registry  *repointel.Registry
	MemoryDir string // absolute path; required to load memory/scan JSON files
	// Manager is optional. When provided the TUI subscribes to its Progress
	// channel to show live step/percentage and failure details.
	Manager *repointel.Manager
}

// ReposTUIRun launches the full-screen Repo Intelligence dashboard.
func ReposTUIRun(cfg ReposTUIConfig) error {
	m := newReposTUIModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ── Model ─────────────────────────────────────────────────────────────────────

type reposTUIModel struct {
	cfg     ReposTUIConfig
	entries []repointel.RepoEntry

	activeTab    int
	selectedRepo int // index into entries

	// Loaded content for selected repo (refreshed when selected repo changes)
	memory    *repointel.RepoMemory
	scan      *repointel.ScanResult
	callGraph *repointel.CallGraph

	// Graph tab state
	graphSelected int // index into callGraph.Nodes

	// Live progress from Manager.Progress channel, keyed by repo ID.
	progress map[string]repointel.ProgressEvent

	search       textinput.Model
	searchActive bool

	viewport viewport.Model
	ready    bool

	spinner spinner.Model
	mode    repoTUIMode

	// huh edit form (Memory tab)
	form          *huh.Form
	editArch      string
	editHints     string
	editUserCtx   string
	editFormError string

	width  int
	height int
}

func newReposTUIModel(cfg ReposTUIConfig) reposTUIModel {
	si := textinput.New()
	si.Placeholder = "Search repos…"
	si.CharLimit = 80

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)

	m := reposTUIModel{
		cfg:      cfg,
		entries:  cfg.Registry.List(),
		search:   si,
		spinner:  sp,
		progress: make(map[string]repointel.ProgressEvent),
		width:    100,
		height:   30,
	}
	m.loadProgressFile()
	m.loadSelectedContent()
	return m
}

// progressListenCmd returns a Cmd that reads one event from Manager.Progress
// and wraps it in a repoProgressMsg. Re-schedules itself after each event.
// Safe to call when Manager is nil.
func progressListenCmd(mgr *repointel.Manager) tea.Cmd {
	if mgr == nil {
		return nil
	}
	return func() tea.Msg {
		ev := <-mgr.Progress
		return repoProgressMsg{ev: ev}
	}
}

// Init satisfies tea.Model.
func (m reposTUIModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, repoRefreshCmd(), progressListenCmd(m.cfg.Manager))
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m reposTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := m.viewportHeight()
		if !m.ready {
			m.viewport = viewport.New(m.width-6, vpH)
			m.ready = true
		} else {
			m.viewport.Width = m.width - 6
			m.viewport.Height = vpH
		}
		m.refreshViewport()

	case repoAutoRefreshMsg:
		m.entries = m.cfg.Registry.List()
		m.loadSelectedContent()
		m.loadProgressFile()
		m.refreshViewport()
		cmds = append(cmds, repoRefreshCmd())

	case repoSaveDoneMsg:
		m.editFormError = ""
		if msg.err != nil {
			m.editFormError = msg.err.Error()
		}
		m.mode = modeBrowse
		m.form = nil
		m.loadSelectedContent()
		m.refreshViewport()

	case repoProgressMsg:
		m.progress[msg.ev.RepoID] = msg.ev
		// Refresh repo list to pick up registry status changes.
		m.entries = m.cfg.Registry.List()
		m.refreshViewport()
		// Re-subscribe for the next event.
		cmds = append(cmds, progressListenCmd(m.cfg.Manager))

	case repoGraphExportDoneMsg:
		m.editFormError = ""
		if msg.err != nil {
			m.editFormError = "export: " + msg.err.Error()
		} else {
			m.editFormError = "Exported: " + msg.path
		}

	case spinner.TickMsg:
		var sc tea.Cmd
		m.spinner, sc = m.spinner.Update(msg)
		cmds = append(cmds, sc)

	case tea.KeyMsg:
		// Edit mode: delegate to huh form.
		if m.mode == modeEdit && m.form != nil {
			if msg.String() == "esc" {
				m.mode = modeBrowse
				m.form = nil
				return m, tea.Batch(cmds...)
			}
			f, fc := m.form.Update(msg)
			m.form = f.(*huh.Form)
			cmds = append(cmds, fc)
			if m.form.State == huh.StateCompleted {
				cmds = append(cmds, m.saveMemoryCmd())
			} else if m.form.State == huh.StateAborted {
				m.mode = modeBrowse
				m.form = nil
			}
			return m, tea.Batch(cmds...)
		}

		// Search mode.
		if m.searchActive {
			switch msg.String() {
			case "esc", "enter":
				m.searchActive = false
				m.search.Blur()
			default:
				var sc tea.Cmd
				m.search, sc = m.search.Update(msg)
				cmds = append(cmds, sc)
			}
			m.refreshViewport()
			return m, tea.Batch(cmds...)
		}

		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit

		case "tab", "right", "l":
			m.activeTab = (m.activeTab + 1) % reposTabCount
			m.viewport.GotoTop()
			m.refreshViewport()

		case "shift+tab", "left", "h":
			m.activeTab = (m.activeTab - 1 + reposTabCount) % reposTabCount
			m.viewport.GotoTop()
			m.refreshViewport()

		case "up", "k":
			if m.activeTab == reposTabRepos && m.selectedRepo > 0 {
				m.selectedRepo--
				m.loadSelectedContent()
				m.refreshViewport()
			} else if m.activeTab == reposTabGraph && m.callGraph != nil && m.graphSelected > 0 {
				m.graphSelected--
				m.refreshViewport()
			} else {
				vp, vc := m.viewport.Update(msg)
				m.viewport = vp
				cmds = append(cmds, vc)
			}

		case "down", "j":
			if m.activeTab == reposTabRepos && m.selectedRepo < len(m.entries)-1 {
				m.selectedRepo++
				m.loadSelectedContent()
				m.refreshViewport()
			} else if m.activeTab == reposTabGraph && m.callGraph != nil && m.graphSelected < len(m.callGraph.Nodes)-1 {
				m.graphSelected++
				m.refreshViewport()
			} else {
				vp, vc := m.viewport.Update(msg)
				m.viewport = vp
				cmds = append(cmds, vc)
			}

		case "pgup", "ctrl+u":
			vp, vc := m.viewport.Update(msg)
			m.viewport = vp
			cmds = append(cmds, vc)

		case "pgdown", "ctrl+d":
			vp, vc := m.viewport.Update(msg)
			m.viewport = vp
			cmds = append(cmds, vc)

		case "/":
			m.searchActive = true
			m.search.Focus()

		case "r":
			m.entries = m.cfg.Registry.List()
			m.loadSelectedContent()
			m.refreshViewport()

		case "e":
			if m.activeTab == reposTabMemory && m.memory != nil {
				mem := m.memory
				m.editArch = mem.Architecture
				m.editHints = mem.ReviewHints
				m.editUserCtx = mem.UserContext
				m.mode = modeEdit
				form := huh.NewForm(
					huh.NewGroup(
						huh.NewText().
							Title("Architecture").
							Description("Correct or augment the AI's understanding of the repo's architecture.").
							Value(&m.editArch),
						huh.NewText().
							Title("Review Focus").
							Description("What reviewers should pay special attention to in PRs.").
							Value(&m.editHints),
						huh.NewText().
							Title("Operator Notes").
							Description("Your own context or domain knowledge. Always injected into PR reviews.").
							Value(&m.editUserCtx),
					),
				).WithTheme(setupTheme())
				m.form = form
				return m, form.Init()
			}

		case "s":
			// Queue selected repo for re-sync via registry (agent picks it up).
			if len(m.entries) > 0 && m.selectedRepo < len(m.entries) {
				e := m.entries[m.selectedRepo]
				_ = m.cfg.Registry.UpdateIndexStatus(e.ID, repointel.IndexPending, "", "")
				_ = m.cfg.Registry.UpdateScanStatus(e.ID, repointel.ScanPending, "", "")
				m.entries = m.cfg.Registry.List()
				m.loadSelectedContent()
				m.refreshViewport()
			}

		case "x":
			// Export call graph as interactive HTML (Graph tab).
			if m.activeTab == reposTabGraph && m.callGraph != nil {
				cg := m.callGraph
				entry := m.selectedEntry()
				htmlPath := filepath.Join(m.cfg.MemoryDir, repointel.SanitiseID(entry.ID)+"-callgraph.html")
				cmds = append(cmds, func() tea.Msg {
					err := repointel.ExportGraphHTML(htmlPath, cg)
					return repoGraphExportDoneMsg{path: htmlPath, err: err}
				})
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m reposTUIModel) View() string {
	if m.mode == modeEdit && m.form != nil {
		return m.viewEditForm()
	}

	chromeBg := lipgloss.NewStyle().
		Background(ColorChromeBg).
		Foreground(ColorMuted).
		Width(m.width).
		Padding(0, 1)

	// ── Context strip ────────────────────────────────────────────────────────
	var statusHint string
	if len(m.entries) > 0 && m.selectedRepo < len(m.entries) {
		e := m.entries[m.selectedRepo]
		// Prefer live progress message from the Manager channel.
		if ev, ok := m.effectiveProgress(e); ok && ev.Kind != repointel.ProgressDone {
			pct := ""
			if ev.Pct() >= 0 {
				pct = fmt.Sprintf(" %d%%", ev.Pct())
			}
			statusHint = "  " + m.spinner.View() + " " + Muted.Render(ev.Message+pct)
		} else {
			switch {
			case e.IndexStatus == repointel.IndexIndexing:
				statusHint = "  " + m.spinner.View() + " " + Muted.Render("indexing "+e.FullName+"…")
			case e.ScanStatus == repointel.ScanScanning:
				statusHint = "  " + m.spinner.View() + " " + Muted.Render("scanning "+e.FullName+"…")
			}
		}
	}
	contextStrip := chromeBg.Render(
		lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Repo Intelligence") +
			"  " + Muted.Render(fmt.Sprintf("%d repos", len(m.entries))) +
			"  " + Muted.Render("mode:"+m.runtimeMode()) + statusHint,
	)

	// ── Tab pills ────────────────────────────────────────────────────────────
	var tabs []string
	for i, label := range reposTabLabels {
		if i == m.activeTab {
			tabs = append(tabs, TabActive.Render(label))
		} else {
			tabs = append(tabs, TabInactive.Render(label))
		}
	}
	tabRow := chromeBg.Render(strings.Join(tabs, " "))

	// ── Search bar ───────────────────────────────────────────────────────────
	searchBorder := ColorBorder
	if m.searchActive {
		searchBorder = ColorAccentLavender
	}
	searchBar := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(searchBorder).
		Background(ColorDashboardBg).
		Width(m.width - 6).
		Padding(0, 1).
		Render(m.search.View())

	divider := Divider(m.width - 2)

	// ── Content ──────────────────────────────────────────────────────────────
	var content string
	if m.activeTab == reposTabRepos {
		// Repos list doesn't use the viewport — it's always short.
		content = lipgloss.NewStyle().
			Background(ColorDashboardBg).
			Width(m.width - 2).
			Padding(1, 2).
			Render(m.renderReposTab(strings.ToLower(m.search.Value())))
	} else {
		// Memory, Scans, Users, Graph go through the viewport for scrollability.
		content = lipgloss.NewStyle().
			Background(ColorDashboardBg).
			Width(m.width - 2).
			Padding(1, 2).
			Render(m.viewport.View())
	}

	// ── Footer ───────────────────────────────────────────────────────────────
	footer := chromeBg.Render(Muted.Render(m.footerHint()))

	return strings.Join([]string{contextStrip, tabRow, searchBar, divider, content, footer}, "\n")
}

func (m reposTUIModel) footerHint() string {
	base := "↑/↓ nav · tab switch · / search · r refresh · s re-sync · esc quit"
	if m.activeTab == reposTabMemory && m.memory != nil {
		base = "↑/↓ scroll · e edit memory · s re-sync · tab switch · esc quit"
	}
	if m.activeTab == reposTabGraph {
		base = "↑/↓ select node · x export HTML · tab switch · esc quit"
	}
	if m.editFormError != "" {
		style := lipgloss.NewStyle().Foreground(ColorError)
		if strings.HasPrefix(m.editFormError, "Exported:") {
			style = lipgloss.NewStyle().Foreground(ColorCyan)
		}
		base = style.Render(m.editFormError) + "  " + base
	}
	return base
}

func (m reposTUIModel) runtimeMode() string {
	if m.cfg.Manager != nil {
		return "live"
	}
	return "file-backed"
}

func (m reposTUIModel) viewEditForm() string {
	if m.form == nil {
		return ""
	}
	entry := m.selectedEntry()
	title := lipgloss.NewStyle().
		Foreground(ColorNeon).Bold(true).
		Render("Edit Memory — " + entry.FullName)
	sub := Muted.Render("  Changes persist to the repo memory JSON and inject into future PR reviews.")
	hint := Muted.Render("  tab/shift+tab navigate · enter confirm · esc cancel")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccentLavender).
		Width(m.width - 4).
		Padding(1, 2).
		Render(title + "\n" + sub + "\n\n" + m.form.View() + "\n" + hint)

	return box
}

// ── Tab renderers ─────────────────────────────────────────────────────────────

func (m reposTUIModel) renderReposTab(query string) string {
	entries := m.filteredEntries(query)
	if len(entries) == 0 {
		return Muted.Render("No repos match. Add one: opsintelligence repos add <owner/name>")
	}

	header := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render(fmt.Sprintf("%-36s  %-10s  %-10s  %-8s  %s", "REPO", "INDEX", "SCAN", "RISK", "LANG"))
	div := Muted.Render(strings.Repeat("─", m.width-8))

	var lines []string
	lines = append(lines, header, div)

	for i, e := range entries {
		risk := nzStr(e.RiskLevel, "—")
		lang := nzStr(e.Language, "—")
		indexSt := string(e.IndexStatus)
		scanSt := string(e.ScanStatus)

		// Colorize status fields.
		indexStyled := m.colorizeStatus(indexSt)
		scanStyled := m.colorizeStatus(scanSt)
		riskStyled := riskColor(e.RiskLevel).Render(risk)

		prefix := "  "
		nameStyle := lipgloss.NewStyle().Foreground(ColorWhite)
		if i == m.selectedRepo {
			prefix = lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("▶ ")
			nameStyle = lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true)
		}

		name := nameStyle.Render(truncate36(e.FullName))
		row := fmt.Sprintf("%s%-34s  %-20s  %-20s  %-18s  %s",
			prefix, name, indexStyled, scanStyled, riskStyled, Muted.Render(lang))
		lines = append(lines, row)

		// Progress bar / error detail line (indented under the repo row).
		if ev, ok := m.effectiveProgress(e); ok {
			switch ev.Kind {
			case repointel.ProgressError:
				errLine := lipgloss.NewStyle().Foreground(ColorError).
					Render("    ✖ " + truncateN(ev.Message, m.width-12))
				lines = append(lines, errLine)
			case repointel.ProgressDone:
				// Show brief done summary; clear on next refresh.
			default:
				if ev.Pct() >= 0 {
					bar := renderProgressBar(ev.Pct(), 20)
					pctLine := Muted.Render("    ") + bar + "  " +
						Muted.Render(fmt.Sprintf("step %d/%d", ev.Step, ev.Total)) + "  " +
						lipgloss.NewStyle().Foreground(ColorNeon).Render(truncateN(ev.Message, m.width-50))
					lines = append(lines, pctLine)
				}
			}
		} else if e.IndexStatus == repointel.IndexError && e.IndexError != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(ColorError).
				Render("    ✖ index: "+truncateN(e.IndexError, m.width-14)))
		} else if e.ScanStatus == repointel.ScanError && e.ScanError != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(ColorError).
				Render("    ✖ scan: "+truncateN(e.ScanError, m.width-13)))
		}
	}
	return strings.Join(lines, "\n")
}

// effectiveProgress returns a known progress event for repo entry e, or a
// synthetic status-derived event when explicit progress payloads are absent.
func (m reposTUIModel) effectiveProgress(e repointel.RepoEntry) (repointel.ProgressEvent, bool) {
	if ev, ok := m.progress[e.ID]; ok {
		return ev, true
	}
	switch {
	case e.IndexStatus == repointel.IndexIndexing:
		return repointel.ProgressEvent{
			RepoID:  e.ID,
			Kind:    repointel.ProgressIndexing,
			Message: "indexing codebase",
			Step:    1,
			Total:   6,
		}, true
	case e.ScanStatus == repointel.ScanScanning:
		return repointel.ProgressEvent{
			RepoID:  e.ID,
			Kind:    repointel.ProgressScanning,
			Message: "scanning for CVEs and bottlenecks",
			Step:    4,
			Total:   6,
		}, true
	default:
		return repointel.ProgressEvent{}, false
	}
}

func (m reposTUIModel) colorizeStatus(s string) string {
	switch s {
	case "ready", "done":
		return lipgloss.NewStyle().Foreground(ColorCyan).Render(s)
	case "indexing", "scanning":
		return lipgloss.NewStyle().Foreground(ColorNeon).Render(m.spinner.View() + " " + s)
	case "error":
		return lipgloss.NewStyle().Foreground(ColorError).Render(s)
	default:
		return Muted.Render(s)
	}
}

func (m reposTUIModel) renderMemoryTab() string {
	entry := m.selectedEntry()
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Memory — " + entry.FullName)

	if entry.IndexStatus == repointel.IndexIndexing {
		return title + "\n\n" + Muted.Render(m.spinner.View()+" Indexing in progress…")
	}
	if entry.IndexStatus != repointel.IndexReady {
		hint := "  Run: opsintelligence repos sync " + entry.FullName + " --platform " + entry.Platform
		return title + "\n\n" +
			Muted.Render("Not yet indexed. Status: "+string(entry.IndexStatus)) + "\n" +
			Muted.Render(hint)
	}
	if m.memory == nil {
		return title + "\n\n" + Muted.Render("Memory file not found. Press s to re-sync.")
	}

	mem := m.memory
	var sb strings.Builder

	sb.WriteString(title + "\n")
	sb.WriteString(Muted.Render(fmt.Sprintf("  Indexed %s · SHA %s · %s",
		fmtTime(entry.IndexedAt), shortSHA(entry.HeadSHA), mem.PrimaryLang)) + "\n")
	sb.WriteString("\n")

	// Architecture
	sb.WriteString(sectionHeader("Architecture"))
	sb.WriteString(wrapText(mem.Architecture, m.width-10) + "\n\n")

	// Languages
	if len(mem.Languages) > 0 {
		sb.WriteString(sectionHeader("Languages"))
		sb.WriteString(Muted.Render("  "+strings.Join(mem.Languages, "  ·  ")) + "\n\n")
	}

	// Conventions
	if len(mem.Conventions) > 0 {
		sb.WriteString(sectionHeader("Coding Conventions"))
		for _, c := range mem.Conventions {
			sb.WriteString(kvLine(c.Name, c.Pattern) + "\n")
		}
		sb.WriteString("\n")
	}

	// Key files
	if len(mem.KeyFiles) > 0 {
		sb.WriteString(sectionHeader("Key Files"))
		for _, f := range mem.KeyFiles {
			sb.WriteString(Muted.Render("  • ") + lipgloss.NewStyle().Foreground(ColorNeon).Render(f) + "\n")
		}
		sb.WriteString("\n")
	}

	// Dependencies
	if len(mem.Dependencies) > 0 {
		sb.WriteString(sectionHeader("Dependencies"))
		for _, d := range mem.Dependencies {
			ver := ""
			if d.Version != "" {
				ver = " " + Muted.Render("@"+d.Version)
			}
			purpose := ""
			if d.Purpose != "" {
				purpose = "  " + Muted.Render(d.Purpose)
			}
			sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorCyan).Render(d.Name) + ver + purpose + "\n")
		}
		sb.WriteString("\n")
	}

	// Review hints
	if mem.ReviewHints != "" {
		sb.WriteString(sectionHeader("Review Focus"))
		sb.WriteString(wrapText(mem.ReviewHints, m.width-10) + "\n\n")
	}

	// Common issues
	if len(mem.CommonIssues) > 0 {
		sb.WriteString(sectionHeader("Common Issue Patterns"))
		for _, issue := range mem.CommonIssues {
			sb.WriteString(lipgloss.NewStyle().Foreground(ColorError).Render("  ⚠ ") + issue + "\n")
		}
		sb.WriteString("\n")
	}

	// Test patterns
	if mem.TestPatterns != "" {
		sb.WriteString(sectionHeader("Test Patterns"))
		sb.WriteString(wrapText(mem.TestPatterns, m.width-10) + "\n\n")
	}

	// CI summary
	if mem.CISummary != "" {
		sb.WriteString(sectionHeader("CI / CD"))
		sb.WriteString(wrapText(mem.CISummary, m.width-10) + "\n\n")
	}

	// Operator notes
	if mem.UserContext != "" {
		sb.WriteString(sectionHeader("Operator Notes"))
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorAccentLavender).
			Render(wrapText(mem.UserContext, m.width-10)) + "\n\n")
	} else {
		sb.WriteString(Muted.Render("  ─  No operator notes. Press e to add your context.") + "\n")
	}

	return sb.String()
}

func (m reposTUIModel) renderScansTab() string {
	entry := m.selectedEntry()
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Security Scan — " + entry.FullName)

	if entry.ScanStatus == repointel.ScanScanning {
		return title + "\n\n" + Muted.Render(m.spinner.View()+" Scan in progress…")
	}
	if entry.ScanStatus != repointel.ScanDone {
		return title + "\n\n" + Muted.Render("Not yet scanned. Status: "+string(entry.ScanStatus)) +
			"\n" + Muted.Render("  Press s to queue a scan.")
	}
	if m.scan == nil {
		return title + "\n\n" + Muted.Render("Scan file not found. Press s to re-scan.")
	}

	sc := m.scan
	var sb strings.Builder

	// Risk banner
	riskLabel := nzStr(sc.RiskLevel, "info")
	riskBanner := riskColor(riskLabel).Bold(true).Padding(0, 2).
		Render("  RISK: " + strings.ToUpper(riskLabel) + "  ")
	sb.WriteString(title + "  " + riskBanner + "\n")
	sb.WriteString(Muted.Render(fmt.Sprintf("  Scanned %s", fmtTime(sc.ScannedAt))) + "\n\n")

	// Summary
	if sc.Summary != "" {
		sb.WriteString(sectionHeader("Summary"))
		sb.WriteString(wrapText(sc.Summary, m.width-10) + "\n\n")
	}

	// CVEs
	if len(sc.CVEs) > 0 {
		sb.WriteString(sectionHeader(fmt.Sprintf("CVEs (%d found)", len(sc.CVEs))))
		for _, cve := range sc.CVEs {
			sevStyle := riskColor(cve.Severity)
			ids := strings.Join(cve.CVEIDs, ", ")
			if ids == "" {
				ids = "no CVE ID"
			}
			sb.WriteString(sevStyle.Render(fmt.Sprintf("  [%s]", strings.ToUpper(cve.Severity))) +
				"  " + lipgloss.NewStyle().Foreground(ColorNeon).Render(cve.Package))
			if cve.Version != "" {
				sb.WriteString(Muted.Render(" @" + cve.Version))
			}
			sb.WriteString("  " + Muted.Render(ids) + "\n")
			sb.WriteString("       " + cve.Description + "\n")
			if cve.Fix != "" {
				sb.WriteString("       " + lipgloss.NewStyle().Foreground(ColorCyan).Render("→ "+cve.Fix) + "\n")
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(sectionHeader("CVEs"))
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorCyan).Render("  ✓ No CVEs found") + "\n\n")
	}

	// Bottlenecks
	if len(sc.Bottlenecks) > 0 {
		sb.WriteString(sectionHeader(fmt.Sprintf("Bottlenecks (%d found)", len(sc.Bottlenecks))))
		for _, b := range sc.Bottlenecks {
			sb.WriteString(riskColor(b.Severity).Render(fmt.Sprintf("  [%s]", strings.ToUpper(b.Severity))) +
				"  " + lipgloss.NewStyle().Foreground(ColorNeon).Render(b.Location) + "\n")
			sb.WriteString("       " + b.Description + "\n")
			if b.Fix != "" {
				sb.WriteString("       " + lipgloss.NewStyle().Foreground(ColorCyan).Render("→ "+b.Fix) + "\n")
			}
			sb.WriteString("\n")
		}
	}

	// Architecture suggestions
	if len(sc.Suggestions) > 0 {
		sb.WriteString(sectionHeader(fmt.Sprintf("Suggestions (%d)", len(sc.Suggestions))))
		for _, s := range sc.Suggestions {
			priBadge := Muted.Render(fmt.Sprintf("[%s]", s.Priority))
			sb.WriteString("  " + priBadge + "  " + lipgloss.NewStyle().Foreground(ColorAccentLavender).Render(s.Area) + "\n")
			sb.WriteString("       " + s.Suggestion + "\n\n")
		}
	}

	return sb.String()
}

func (m reposTUIModel) renderGraphTab() string {
	entry := m.selectedEntry()
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Call Graph — " + entry.FullName)

	if entry.IndexStatus != repointel.IndexReady {
		return title + "\n\n" + Muted.Render("Not yet indexed. Press s to sync.")
	}
	if m.callGraph == nil {
		return title + "\n\n" + Muted.Render("No call graph available. Graph is built during indexing.\n  Press s to re-sync and build the call graph.")
	}

	cg := m.callGraph
	nodes := cg.Nodes

	var sb strings.Builder
	sb.WriteString(title + "\n")
	sb.WriteString(Muted.Render(fmt.Sprintf("  %d functions · %d call relationships · press x to export interactive HTML",
		len(nodes), len(cg.Edges))) + "\n\n")

	if len(nodes) == 0 {
		sb.WriteString(Muted.Render("  No function definitions found in supported languages.") + "\n")
		return sb.String()
	}

	// Clamp selected index.
	sel := m.graphSelected
	if sel >= len(nodes) {
		sel = 0
	}
	selNode := nodes[sel]

	// ── Left column: node list ─────────────────────────────────────────────
	listW := m.width/3 - 4
	if listW < 20 {
		listW = 20
	}
	rightW := m.width - listW - 10

	var listLines []string
	// Show a window of 20 nodes around selection.
	winStart := sel - 10
	if winStart < 0 {
		winStart = 0
	}
	winEnd := winStart + 20
	if winEnd > len(nodes) {
		winEnd = len(nodes)
		winStart = winEnd - 20
		if winStart < 0 {
			winStart = 0
		}
	}

	listLines = append(listLines, lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).Render("  Functions"))
	listLines = append(listLines, Muted.Render("  "+strings.Repeat("─", listW-2)))
	if winStart > 0 {
		listLines = append(listLines, Muted.Render(fmt.Sprintf("  ↑ %d more…", winStart)))
	}
	for i := winStart; i < winEnd; i++ {
		n := nodes[i]
		label := truncateN(n.Name, listW-4)
		if i == sel {
			listLines = append(listLines, lipgloss.NewStyle().
				Foreground(ColorAccentLavender).Bold(true).
				Render("▶ "+label))
		} else {
			listLines = append(listLines, Muted.Render("  ")+
				lipgloss.NewStyle().Foreground(ColorWhite).Render(label))
		}
	}
	if winEnd < len(nodes) {
		listLines = append(listLines, Muted.Render(fmt.Sprintf("  ↓ %d more…", len(nodes)-winEnd)))
	}

	// ── Right column: node detail + neighbors ─────────────────────────────
	callerIDs := cg.Callers(selNode.ID)
	calleeIDs := cg.Callees(selNode.ID)

	var detailLines []string
	detailLines = append(detailLines,
		lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).Render("  "+selNode.Name))
	detailLines = append(detailLines,
		Muted.Render(fmt.Sprintf("  %s  line %d", selNode.File, selNode.Line)))
	if selNode.Package != "" {
		detailLines = append(detailLines, Muted.Render("  pkg: "+selNode.Package))
	}
	detailLines = append(detailLines, "")

	// Called by (callers).
	detailLines = append(detailLines,
		lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render("  Called by"))
	if len(callerIDs) == 0 {
		detailLines = append(detailLines, Muted.Render("  (entry point or no callers found)"))
	} else {
		for _, cid := range callerIDs {
			if cn, ok := cg.NodeByID(cid); ok {
				detailLines = append(detailLines,
					Muted.Render("  ← ")+lipgloss.NewStyle().Foreground(ColorWhite).Render(truncateN(cn.Name, rightW-6))+
						Muted.Render("  "+truncateN(cn.File, rightW/2)))
			}
		}
	}
	detailLines = append(detailLines, "")

	// Calls (callees).
	detailLines = append(detailLines,
		lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).Render("  Calls"))
	if len(calleeIDs) == 0 {
		detailLines = append(detailLines, Muted.Render("  (leaf function — no outgoing calls found)"))
	} else {
		for _, cid := range calleeIDs {
			if cn, ok := cg.NodeByID(cid); ok {
				detailLines = append(detailLines,
					Muted.Render("  → ")+lipgloss.NewStyle().Foreground(ColorWhite).Render(truncateN(cn.Name, rightW-6))+
						Muted.Render("  "+truncateN(cn.File, rightW/2)))
			}
		}
	}
	detailLines = append(detailLines, "")

	// ── ASCII mini-graph ──────────────────────────────────────────────────
	detailLines = append(detailLines,
		lipgloss.NewStyle().Foreground(ColorMuted).Bold(true).Render("  Neighborhood"))
	detailLines = append(detailLines, Muted.Render("  "+strings.Repeat("─", rightW-4)))
	detailLines = append(detailLines, m.renderMiniGraph(selNode, cg, rightW))

	// ── Merge columns side by side ────────────────────────────────────────
	maxRows := len(listLines)
	if len(detailLines) > maxRows {
		maxRows = len(detailLines)
	}
	// Pad shorter column.
	for len(listLines) < maxRows {
		listLines = append(listLines, "")
	}
	for len(detailLines) < maxRows {
		detailLines = append(detailLines, "")
	}

	colLeft := lipgloss.NewStyle().Width(listW)
	colRight := lipgloss.NewStyle().Width(rightW)

	for i := 0; i < maxRows; i++ {
		left := colLeft.Render(listLines[i])
		right := colRight.Render(detailLines[i])
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right) + "\n")
	}

	return sb.String()
}

// renderMiniGraph draws a compact ASCII node-link diagram for the selected
// node and its direct neighbours (1-hop callers + callees).
func (m reposTUIModel) renderMiniGraph(center repointel.CallNode, cg *repointel.CallGraph, width int) string {
	callerIDs := cg.Callers(center.ID)
	calleeIDs := cg.Callees(center.ID)

	// Limit display to 4 callers + 4 callees.
	if len(callerIDs) > 4 {
		callerIDs = callerIDs[:4]
	}
	if len(calleeIDs) > 4 {
		calleeIDs = calleeIDs[:4]
	}

	centerLabel := "[ " + truncateN(center.Name, 20) + " ]"
	centerStyle := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true)

	var lines []string

	// Callers above center.
	for _, cid := range callerIDs {
		if cn, ok := cg.NodeByID(cid); ok {
			label := truncateN(cn.Name, 18)
			lines = append(lines,
				Muted.Render("  ( "+label+" )")+"  "+
					lipgloss.NewStyle().Foreground(ColorCyan).Render("─calls─▶"))
		}
	}
	if len(callerIDs) > 0 {
		lines = append(lines, "  "+strings.Repeat(" ", len(centerLabel)/2)+"│")
	}

	lines = append(lines, "  "+centerStyle.Render(centerLabel))

	// Callees below center.
	if len(calleeIDs) > 0 {
		lines = append(lines, "  "+strings.Repeat(" ", len(centerLabel)/2)+"│")
	}
	for _, cid := range calleeIDs {
		if cn, ok := cg.NodeByID(cid); ok {
			label := truncateN(cn.Name, 18)
			lines = append(lines,
				Muted.Render("  ▶")+" "+
					lipgloss.NewStyle().Foreground(ColorNeon).Render("( "+label+" )"))
		}
	}

	if len(callerIDs) == 0 && len(calleeIDs) == 0 {
		lines = append(lines, Muted.Render("  (isolated node — no connections)"))
	}

	return strings.Join(lines, "\n")
}

func truncateN(s string, n int) string {
	if n <= 3 {
		return s
	}
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func (m reposTUIModel) renderUsersTab() string {
	entry := m.selectedEntry()
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).
		Render("Users — " + entry.FullName)

	if len(entry.Users) == 0 {
		return title + "\n\n" + Muted.Render("No users configured.\n  Add: opsintelligence repos users add "+entry.FullName+" <handle> --role reviewer")
	}

	var sb strings.Builder
	sb.WriteString(title + "\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorAccentLavender).
		Render(fmt.Sprintf("  %-22s  %-14s  %s", "HANDLE", "ROLE", "EMAIL")) + "\n")
	sb.WriteString(Muted.Render("  " + strings.Repeat("─", 54)) + "\n")

	for _, u := range entry.Users {
		email := nzStr(u.Email, "—")
		sb.WriteString(fmt.Sprintf("  %-22s  %-14s  %s\n",
			lipgloss.NewStyle().Foreground(ColorWhite).Render(u.Handle),
			Muted.Render(string(u.Role)),
			Muted.Render(email),
		))
	}
	return sb.String()
}

// ── Viewport management ───────────────────────────────────────────────────────

func (m *reposTUIModel) refreshViewport() {
	if !m.ready {
		return
	}
	var content string
	query := strings.ToLower(m.search.Value())
	switch m.activeTab {
	case reposTabMemory:
		content = m.renderMemoryTab()
	case reposTabScans:
		content = m.renderScansTab()
	case reposTabUsers:
		content = m.renderUsersTab()
	case reposTabGraph:
		content = m.renderGraphTab()
	default:
		content = m.renderReposTab(query) // fallback; not actually used in viewport path
	}
	m.viewport.SetContent(content)
}

func (m reposTUIModel) viewportHeight() int {
	// chrome strip + tab row + search bar + divider + footer ≈ 6 rows
	h := m.height - 8
	if h < 6 {
		h = 6
	}
	return h
}

// ── Lazy content loading ──────────────────────────────────────────────────────

func (m *reposTUIModel) loadSelectedContent() {
	m.memory = nil
	m.scan = nil
	m.callGraph = nil
	m.graphSelected = 0
	if len(m.entries) == 0 || m.selectedRepo >= len(m.entries) {
		return
	}
	e := m.entries[m.selectedRepo]
	if e.MemoryFile != "" && e.IndexStatus == repointel.IndexReady {
		abs := filepath.Join(m.cfg.MemoryDir, e.MemoryFile)
		mem, err := repointel.LoadMemory(abs)
		if err == nil {
			m.memory = mem
		}
	}
	if e.ScanFile != "" && e.ScanStatus == repointel.ScanDone {
		abs := filepath.Join(m.cfg.MemoryDir, e.ScanFile)
		sc, err := repointel.LoadScan(abs)
		if err == nil {
			m.scan = sc
		}
	}
	if e.CallGraphFile != "" {
		abs := filepath.Join(m.cfg.MemoryDir, e.CallGraphFile)
		cg, err := repointel.LoadCallGraph(abs)
		if err == nil {
			m.callGraph = cg
		}
	}
}

// ── Progress file ─────────────────────────────────────────────────────────────

// loadProgressFile reads the cross-process progress.json written by the Manager.
// It merges new events into m.progress, clearing done/error events for repos
// that have since reached a terminal registry status.
func (m *reposTUIModel) loadProgressFile() {
	path := filepath.Join(m.cfg.MemoryDir, "progress.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state map[string]repointel.ProgressEvent
	if err := json.Unmarshal(b, &state); err != nil {
		return
	}
	for id, ev := range state {
		// Clear progress display once the registry reflects a terminal state
		// and enough time has passed (done events older than 10s).
		if ev.Kind == repointel.ProgressDone {
			m.progress[id] = ev
		} else if ev.Kind == repointel.ProgressError {
			m.progress[id] = ev
		} else {
			m.progress[id] = ev
		}
	}
	// Purge progress entries for repos that are now fully done in the registry
	// and whose progress event is ProgressDone (avoids stale spinner display).
	entryByID := make(map[string]repointel.RepoEntry, len(m.entries))
	for _, e := range m.entries {
		entryByID[e.ID] = e
	}
	for id, ev := range m.progress {
		if ev.Kind == repointel.ProgressDone {
			if e, ok := entryByID[id]; ok {
				if e.IndexStatus == repointel.IndexReady && e.ScanStatus == repointel.ScanDone {
					delete(m.progress, id)
				}
			}
		}
	}
}

// ── Edit form ─────────────────────────────────────────────────────────────────


func (m reposTUIModel) saveMemoryCmd() tea.Cmd {
	return func() tea.Msg {
		if m.memory == nil {
			return repoSaveDoneMsg{err: fmt.Errorf("memory not loaded")}
		}
		mem := *m.memory // copy
		mem.Architecture = m.editArch
		mem.ReviewHints = m.editHints
		mem.UserContext = m.editUserCtx
		mem.UpdatedAt = time.Now().UTC()

		entry := m.selectedEntry()
		if entry.MemoryFile == "" {
			return repoSaveDoneMsg{err: fmt.Errorf("no memory file path stored for repo")}
		}
		abs := filepath.Join(m.cfg.MemoryDir, entry.MemoryFile)
		err := repointel.SaveMemory(abs, &mem)
		return repoSaveDoneMsg{err: err}
	}
}

// ── Small helpers ─────────────────────────────────────────────────────────────

func (m reposTUIModel) selectedEntry() repointel.RepoEntry {
	if len(m.entries) == 0 {
		return repointel.RepoEntry{}
	}
	idx := m.selectedRepo
	if idx >= len(m.entries) {
		idx = 0
	}
	return m.entries[idx]
}

func (m reposTUIModel) filteredEntries(query string) []repointel.RepoEntry {
	if query == "" {
		return m.entries
	}
	var out []repointel.RepoEntry
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.FullName), query) ||
			strings.Contains(strings.ToLower(e.Language), query) ||
			strings.Contains(strings.ToLower(string(e.IndexStatus)), query) {
			out = append(out, e)
		}
	}
	return out
}

// renderProgressBar renders a Unicode block-character progress bar.
// width is the number of bar characters; pct is 0-100.
func renderProgressBar(pct, width int) string {
	filled := (pct * width) / 100
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	color := ColorCyan
	if pct >= 100 {
		color = lipgloss.Color("#3fb950")
	}
	return lipgloss.NewStyle().Foreground(color).Render("["+bar+"]") +
		lipgloss.NewStyle().Foreground(ColorMuted).Render(fmt.Sprintf(" %3d%%", pct))
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

func sectionHeader(title string) string {
	return lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).Render("  "+title) +
		"\n" + Muted.Render("  "+strings.Repeat("─", len(title)+2)) + "\n"
}

func kvLine(k, v string) string {
	return lipgloss.NewStyle().Foreground(ColorMuted).Width(24).Render("  "+k) +
		lipgloss.NewStyle().Foreground(ColorWhite).Render(v)
}

func wrapText(s string, width int) string {
	if width <= 0 {
		width = 80
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	cur := "  "
	for _, w := range words {
		if len(cur)+len(w)+1 > width {
			lines = append(lines, cur)
			cur = "  " + w
		} else {
			if cur == "  " {
				cur += w
			} else {
				cur += " " + w
			}
		}
	}
	if cur != "  " {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

func nzStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func shortSHA(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

func truncate36(s string) string {
	if len(s) <= 36 {
		return s
	}
	return s[:33] + "..."
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04 UTC")
}
