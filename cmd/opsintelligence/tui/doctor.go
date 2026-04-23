package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DoctorCheck reflects the struct in doctor_cmd.go without creating an import cycle.
type DoctorCheck struct {
	ID       string
	Severity string // ok | warn | error | skipped
	Message  string
}

type doctorResultMsg struct {
	checks []DoctorCheck
}

type DoctorModel struct {
	checks   []DoctorCheck
	running  bool
	spinner  spinner.Model
	width    int
	height   int
	err      string
	runCheck func() []DoctorCheck
}

func NewDoctorModel(runCheck func() []DoctorCheck) DoctorModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorCyan)
	return DoctorModel{
		running:  true,
		spinner:  s,
		runCheck: runCheck,
	}
}

func (m DoctorModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			return doctorResultMsg{checks: m.runCheck()}
		},
	)
}

func (m DoctorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case doctorResultMsg:
		m.checks = msg.checks
		m.running = false
		// We don't quit automatically so the user can see the results
	}

	return m, nil
}

func (m DoctorModel) View() string {
	bold := lipgloss.NewStyle().Bold(true).Foreground(ColorNeon)
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	prim := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	header := bold.Render("  OpsIntelligence Doctor Dashboard")
	if m.running {
		header += " " + m.spinner.View() + dim.Render(" Running checks...")
	} else {
		header += okStyle.Render("  ✔ Checks complete")
	}

	var rows []string
	if len(m.checks) == 0 && m.running {
		rows = append(rows, dim.Render("  Initializing..."))
	}

	for _, c := range m.checks {
		icon := "  • "
		style := dim
		switch c.Severity {
		case "ok":
			icon = okStyle.Render("  ✔ ")
			style = lipgloss.NewStyle().Foreground(ColorWhite)
		case "warn":
			icon = warnStyle.Render("  ⚠ ")
			style = warnStyle
		case "error":
			icon = errStyle.Render("  ✗ ")
			style = errStyle
		case "skipped":
			icon = dim.Render("  ⊘ ")
			style = dim
		}
		rows = append(rows, icon+prim.Render(fmt.Sprintf("%-20s", c.ID))+" "+style.Render(c.Message))
	}

	if !m.running {
		rows = append(rows, "", dim.Render("  Press q to quit"))
	}

	body := strings.Join(rows, "\n")

	return "\n" + lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(1, 4).
		Render(header+"\n\n"+body) + "\n"
}

var (
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// RunDoctor launches the doctor dashboard.
func RunDoctor(runCheck func() []DoctorCheck) error {
	p := tea.NewProgram(NewDoctorModel(runCheck), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
