package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type LogEvent struct {
	Level     string `json:"level"`
	Timestamp string `json:"ts"`
	Message   string `json:"msg"`
	Iteration int    `json:"iteration,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Chain     string `json:"chain,omitempty"`
	Task      string `json:"task,omitempty"`
}

type monitorLogMsg struct {
	events []LogEvent
}

type MonitorModel struct {
	info       StatusInfo
	ps         psResult
	events     []LogEvent
	width      int
	height     int
	logPath    string
	lastOffset int64
}

func NewMonitorModel(info StatusInfo, logPath string) MonitorModel {
	return MonitorModel{
		info:    info,
		logPath: logPath,
	}
}

func (m MonitorModel) Init() tea.Cmd {
	return tea.Batch(
		fetchPS(m.info.PID),
		tickEvery(),
		m.pollLog(),
	)
}

func (m MonitorModel) pollLog() tea.Cmd {
	return func() tea.Msg {
		file, err := os.Open(m.logPath)
		if err != nil {
			return monitorLogMsg{}
		}
		defer file.Close()

		stat, _ := file.Stat()
		if m.lastOffset == 0 {
			// Start by reading the last 4KB
			m.lastOffset = stat.Size() - 4096
			if m.lastOffset < 0 {
				m.lastOffset = 0
			}
		}

		if stat.Size() <= m.lastOffset {
			return monitorLogMsg{}
		}

		file.Seek(m.lastOffset, 0)
		data := make([]byte, stat.Size()-m.lastOffset)
		file.Read(data)
		m.lastOffset = stat.Size()

		lines := strings.Split(string(data), "\n")
		var events []LogEvent
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev LogEvent
			if err := json.Unmarshal([]byte(line), &ev); err == nil {
				events = append(events, ev)
			}
		}
		return monitorLogMsg{events: events}
	}
}

func (m MonitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}

	case tickMsg:
		return m, tea.Batch(fetchPS(m.info.PID), tickEvery(), m.pollLog())

	case psResult:
		m.ps = msg

	case monitorLogMsg:
		m.events = append(m.events, msg.events...)
		if len(m.events) > 20 { // Keep last 20 events for the view
			m.events = m.events[len(m.events)-20:]
		}
	}

	return m, nil
}

func (m MonitorModel) renderLogTable() string {
	bold := lipgloss.NewStyle().Bold(true).Foreground(ColorNeon)
	dim := lipgloss.NewStyle().Foreground(ColorMuted)

	header := lipgloss.JoinHorizontal(lipgloss.Left,
		bold.Width(10).Render("TIME"),
		bold.Width(6).Render("ITER"),
		bold.Width(40).Render("EVENT"),
		bold.Width(20).Render("TOOL/CHAIN"),
	)

	var rows []string
	rows = append(rows, header)
	rows = append(rows, dim.Render(strings.Repeat("─", 76)))

	for _, ev := range m.events {
		ts := ""
		if len(ev.Timestamp) > 19 {
			ts = ev.Timestamp[11:19]
		}
		iter := fmt.Sprintf("%d", ev.Iteration)
		if ev.Iteration == 0 {
			iter = "-"
		}
		tool := ev.Tool
		if tool == "" {
			tool = ev.Chain
		}

		row := lipgloss.JoinHorizontal(lipgloss.Left,
			dim.Width(10).Render(ts),
			dim.Width(6).Render(iter),
			lipgloss.NewStyle().Width(40).Render(ev.Message),
			lipgloss.NewStyle().Foreground(ColorPrimary).Width(20).Render(tool),
		)
		rows = append(rows, row)
	}

	return strings.Join(rows, "\n")
}

func (m MonitorModel) View() string {
	bold := lipgloss.NewStyle().Bold(true).Foreground(ColorNeon)
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	prim := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	statusLine := StatusOK + " " + prim.Render("MONITORING") + " " + dim.Render(m.info.Version)
	if m.ps.alive {
		statusLine += dim.Render(fmt.Sprintf("   PID %d   %s", m.info.PID, m.ps.etime))
	} else {
		statusLine = StatusErr + " " + lipgloss.NewStyle().Foreground(ColorError).Bold(true).Render("ORCHESTRATOR STOPPED")
	}

	cpuPct := 0.0
	fmt.Sscanf(m.ps.cpu, "%f", &cpuPct)
	ramMB := float64(m.ps.rssKB) / 1024.0
	stats := dim.Render("CPU ") + ProgressBar(cpuPct, 10) + fmt.Sprintf(" %.1f%%  ", cpuPct) +
		dim.Render("RAM ") + ProgressBar((ramMB/1024)*100, 10) + fmt.Sprintf(" %.0fMB", ramMB)

	header := lipgloss.JoinVertical(lipgloss.Left,
		statusLine,
		"",
		stats,
		"",
		bold.Render("  Live Run Trace"),
	)

	return "\n " + lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(1, 2).
		Render(header+"\n"+m.renderLogTable()+"\n\n "+dim.Render("q to quit")) + "\n"
}

// RunMonitor launches the live monitoring dashboard.
func RunMonitor(info StatusInfo, logPath string) error {
	p := tea.NewProgram(NewMonitorModel(info, logPath), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
