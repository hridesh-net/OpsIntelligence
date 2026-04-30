package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OnboardSummary holds the collected config values to display after onboarding.
type OnboardSummary struct {
	// Provider
	PrimaryProvider string
	PrimaryModel    string
	SecondaryProv   string
	EmbedProvider   string
	EmbedModel      string

	// System
	GatewayHost       string
	GatewayPort       int
	GatewayMode       string
	LocalIntelEnabled bool
	LocalIntelGGUF    string
	MemPalaceEnabled  bool
	PlanoEnabled      bool
	PlanoEndpoint     string

	// Channels
	Channels []string

	// DevOps
	GitHubEnabled  bool
	GitHubOrg      string
	GitLabEnabled  bool
	GitLabURL      string
	JenkinsEnabled bool
	JenkinsURL     string
	SonarEnabled   bool
	SonarURL       string

	// Skills
	Skills []string
}

const (
	onboardTabOverview = iota
	onboardTabProvider
	onboardTabChannels
	onboardTabDevOps
	onboardTabSystem
	onboardTabCount
)

// OnboardSummaryModel is a Claude-style tabbed review panel shown at the end of onboarding.
type OnboardSummaryModel struct {
	summary       OnboardSummary
	activeTab     int
	width, height int
	search        textinput.Model
	searchActive  bool
}

// NewOnboardSummaryModel builds the onboard summary TUI.
func NewOnboardSummaryModel(s OnboardSummary) *OnboardSummaryModel {
	ti := textinput.New()
	ti.Placeholder = "Search settings..."
	ti.CharLimit = 64
	ti.Width = 40
	ti.PromptStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	ti.TextStyle = lipgloss.NewStyle().Foreground(ColorWhite)
	return &OnboardSummaryModel{
		summary: s,
		search:  ti,
	}
}

func (m *OnboardSummaryModel) Init() tea.Cmd { return nil }

func (m *OnboardSummaryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			return m, tea.Quit
		case "/":
			m.searchActive = true
			m.search.Focus()
			return m, textinput.Blink
		case "left", "shift+tab":
			m.activeTab = (m.activeTab - 1 + onboardTabCount) % onboardTabCount
		case "right", "tab":
			m.activeTab = (m.activeTab + 1) % onboardTabCount
		}
	}
	return m, nil
}

func (m *OnboardSummaryModel) View() string {
	if m.width <= 0 {
		m.width = 100
	}
	w := m.width

	// Context strip
	strip := lipgloss.NewStyle().Width(w).Background(ColorChromeBg).Padding(0, 1).Render(
		ChromePrompt.Render("›") + " " +
			lipgloss.NewStyle().Background(ColorChromeBg).Foreground(ColorWhite).Render("/onboard"),
	)

	// Tab row
	tabNames := []string{"Overview", "Provider", "Channels", "DevOps", "System"}
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

	// Search bar
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

	// Divider
	divLine := strings.Repeat("─", max(0, w-2))
	div := lipgloss.NewStyle().Width(w).Padding(0, 1).Render(DashboardDivider.Render(divLine))

	// Body
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	body := m.renderTab(query)
	contentH := m.height - 7
	if contentH < 4 {
		contentH = 4
	}
	body = clipDashboardLines(body, contentH)
	panel := DashboardPanel.Width(w - 2).Render(body)

	// Footer
	footer := lipgloss.NewStyle().Width(w).Background(ColorDashboardBg).Padding(0, 1).
		Foreground(ColorMuted).Render("↑/↓/tab to switch · / to search · Esc to continue")

	return lipgloss.JoinVertical(lipgloss.Left, strip, tabRow, searchBar, div, panel, footer)
}

func (m *OnboardSummaryModel) renderTab(query string) string {
	switch m.activeTab {
	case onboardTabOverview:
		return m.renderOverview(query)
	case onboardTabProvider:
		return m.renderProvider(query)
	case onboardTabChannels:
		return m.renderChannels(query)
	case onboardTabDevOps:
		return m.renderDevOps(query)
	case onboardTabSystem:
		return m.renderSystem(query)
	}
	return ""
}

func kvRows(query string, kv []struct{ k, v string }) []string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender)
	const keyW = 24
	var out []string
	matched := 0
	for _, row := range kv {
		if query != "" && !strings.Contains(row.k, query) && !strings.Contains(strings.ToLower(row.v), query) {
			continue
		}
		matched++
		pad := strings.Repeat(" ", max(0, keyW-len(row.k)))
		out = append(out, dim.Render(row.k+pad)+lav.Render(row.v))
	}
	if query != "" && matched == 0 {
		out = append(out, dim.Render("No matches for "+strconv.Quote(query)))
	}
	return out
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (m *OnboardSummaryModel) renderOverview(query string) string {
	s := m.summary
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Overview")
	channels := "none"
	if len(s.Channels) > 0 {
		channels = strings.Join(s.Channels, ", ")
	}
	skills := "none"
	if len(s.Skills) > 0 {
		skills = strings.Join(s.Skills, ", ")
	}
	kv := []struct{ k, v string }{
		{"primary_provider", nz(s.PrimaryProvider, "—")},
		{"primary_model", nz(s.PrimaryModel, "—")},
		{"embed_provider", nz(s.EmbedProvider, "—")},
		{"gateway", fmt.Sprintf("%s:%d (%s)", s.GatewayHost, s.GatewayPort, nz(s.GatewayMode, "loopback"))},
		{"channels", channels},
		{"skills", skills},
		{"mempalace", boolStr(s.MemPalaceEnabled)},
		{"local_intel", boolStr(s.LocalIntelEnabled)},
	}
	rows := append([]string{title, ""}, kvRows(query, kv)...)
	return strings.Join(rows, "\n")
}

func (m *OnboardSummaryModel) renderProvider(query string) string {
	s := m.summary
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Provider")
	secondary := nz(s.SecondaryProv, "none")
	kv := []struct{ k, v string }{
		{"primary", nz(s.PrimaryProvider, "—")},
		{"primary.default_model", nz(s.PrimaryModel, "—")},
		{"secondary", secondary},
		{"embed.provider", nz(s.EmbedProvider, "—")},
		{"embed.default_model", nz(s.EmbedModel, "—")},
	}
	rows := append([]string{title, ""}, kvRows(query, kv)...)
	return strings.Join(rows, "\n")
}

func (m *OnboardSummaryModel) renderChannels(query string) string {
	s := m.summary
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("Channels")
	allChannels := []string{"telegram", "discord", "slack", "whatsapp"}
	enabled := make(map[string]bool)
	for _, ch := range s.Channels {
		enabled[ch] = true
	}
	var kv []struct{ k, v string }
	for _, ch := range allChannels {
		kv = append(kv, struct{ k, v string }{ch, boolStr(enabled[ch])})
	}
	rows := append([]string{title, ""}, kvRows(query, kv)...)
	return strings.Join(rows, "\n")
}

func (m *OnboardSummaryModel) renderDevOps(query string) string {
	s := m.summary
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("DevOps")
	kv := []struct{ k, v string }{
		{"github.enabled", boolStr(s.GitHubEnabled)},
		{"github.default_org", nz(s.GitHubOrg, "—")},
		{"gitlab.enabled", boolStr(s.GitLabEnabled)},
		{"gitlab.base_url", nz(s.GitLabURL, "—")},
		{"jenkins.enabled", boolStr(s.JenkinsEnabled)},
		{"jenkins.base_url", nz(s.JenkinsURL, "—")},
		{"sonar.enabled", boolStr(s.SonarEnabled)},
		{"sonar.base_url", nz(s.SonarURL, "—")},
	}
	rows := append([]string{title, ""}, kvRows(query, kv)...)
	return strings.Join(rows, "\n")
}

func (m *OnboardSummaryModel) renderSystem(query string) string {
	s := m.summary
	title := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true).Render("System")
	planoEndpoint := nz(s.PlanoEndpoint, "—")
	gguf := nz(s.LocalIntelGGUF, "—")
	kv := []struct{ k, v string }{
		{"gateway.host", nz(s.GatewayHost, "—")},
		{"gateway.port", fmt.Sprintf("%d", s.GatewayPort)},
		{"gateway.mode", nz(s.GatewayMode, "loopback")},
		{"plano.enabled", boolStr(s.PlanoEnabled)},
		{"plano.endpoint", planoEndpoint},
		{"local_intel.enabled", boolStr(s.LocalIntelEnabled)},
		{"local_intel.gguf", gguf},
		{"mempalace.enabled", boolStr(s.MemPalaceEnabled)},
	}
	rows := append([]string{title, ""}, kvRows(query, kv)...)
	return strings.Join(rows, "\n")
}

// RunOnboardSummary launches the full-screen onboard summary TUI.
func RunOnboardSummary(s OnboardSummary) error {
	m := NewOnboardSummaryModel(s)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
