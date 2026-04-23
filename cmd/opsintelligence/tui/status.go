package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─────────────────────────────────────────────
// Public config passed in from statusCmd
// ─────────────────────────────────────────────

// StatusInfo holds the static config that doesn't change between refreshes.
type StatusInfo struct {
	PID           int
	Version       string
	SkillSummary  string
	Channels      []string
	PlanoEnabled  bool
	PlanoEndpoint string
	MCPEnabled    bool
	MCPTransport  string
	// GatewayBase is e.g. http://127.0.0.1:18790 (for dashboard / health hints).
	GatewayBase  string
	GatewayBind  string
	RunTraceFile string
	RunTraceMode string
}

// ─────────────────────────────────────────────
// Tea messages
// ─────────────────────────────────────────────

type tickMsg time.Time

type psResult struct {
	cpu   string
	rssKB int64
	vsz   string
	etime string
	alive bool
}

// ─────────────────────────────────────────────
// Model
// ─────────────────────────────────────────────

type StatusModel struct {
	info  StatusInfo
	ps    psResult
	width int
	err   string
}

func NewStatusModel(info StatusInfo) StatusModel {
	return StatusModel{info: info}
}

func (m StatusModel) Init() tea.Cmd {
	return tea.Batch(
		fetchPS(m.info.PID), // immediate first fetch
		tickEvery(),
	)
}

func (m StatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c", "esc":
			return m, tea.Quit
		}

	case tickMsg:
		return m, tea.Batch(fetchPS(m.info.PID), tickEvery())

	case psResult:
		m.ps = msg
	}
	return m, nil
}

// StatusContentLines returns the status dashboard body lines (no outer frame, no quit hint).
// useLavenderBars selects ProgressBarLavender vs ProgressBar for CPU/RAM.
func StatusContentLines(info StatusInfo, ps psResult, useLavenderBars bool) []string {
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	prim := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	lav := lipgloss.NewStyle().Foreground(ColorAccentLavender).Bold(true)

	bar := ProgressBar
	if useLavenderBars {
		bar = ProgressBarLavender
	}

	var statusLine string
	if ps.alive {
		statusLine = StatusOK + " " + lav.Render("RUNNING") +
			dim.Render(fmt.Sprintf("   PID %d   %s", info.PID, ps.etime))
	} else {
		statusLine = StatusErr + " " + lipgloss.NewStyle().Foreground(ColorError).Bold(true).Render("STOPPED")
	}

	cpuPct := 0.0
	fmt.Sscanf(ps.cpu, "%f", &cpuPct)
	ramMB := float64(ps.rssKB) / 1024.0
	ramPct := (ramMB / 1024.0) * 100.0
	if ramPct > 100 {
		ramPct = 100
	}

	cpuLine := dim.Render("CPU ") + bar(cpuPct, 14) + dim.Render(fmt.Sprintf("  %.1f%%", cpuPct))
	ramLine := dim.Render("RAM ") + bar(ramPct, 14) + dim.Render(fmt.Sprintf("  %.1f MB", ramMB))

	channelStr := dim.Render("none")
	if len(info.Channels) > 0 {
		colored := make([]string, len(info.Channels))
		for i, ch := range info.Channels {
			colored[i] = prim.Render(ch)
		}
		channelStr = strings.Join(colored, dim.Render(" · "))
	}

	planoStr := dim.Render("disabled")
	if info.PlanoEnabled {
		planoStr = prim.Render("✓ ") + dim.Render(info.PlanoEndpoint)
	}

	mcpStr := dim.Render("disabled")
	if info.MCPEnabled {
		t := info.MCPTransport
		if t == "" {
			t = "stdio"
		}
		mcpStr = prim.Render("✓ ") + dim.Render(t)
	}

	rows := []string{
		statusLine,
		"",
		dim.Render("  version     ") + prim.Render(info.Version),
		dim.Render("  skills      ") + prim.Render(info.SkillSummary),
		"",
		"  " + cpuLine,
		"  " + ramLine,
		"",
		dim.Render("  channels    ") + channelStr,
		dim.Render("  plano       ") + planoStr,
		dim.Render("  mcp         ") + mcpStr,
	}

	if strings.TrimSpace(info.GatewayBase) != "" {
		base := strings.TrimSuffix(strings.TrimSpace(info.GatewayBase), "/")
		rows = append(rows, "",
			dim.Render("  dashboard   ")+prim.Render(base+"/dashboard/"),
			dim.Render("  health      ")+dim.Render("curl -sS "+base+"/health"),
		)
		if b := strings.TrimSpace(info.GatewayBind); b != "" && b != "loopback" {
			rows = append(rows, dim.Render("  gateway.bind ")+prim.Render(b))
		}
	}
	if strings.TrimSpace(info.RunTraceFile) != "" && strings.TrimSpace(info.RunTraceMode) != "off" {
		rows = append(rows,
			"",
			dim.Render("  run trace   ")+dim.Render("tail -f "+strings.TrimSpace(info.RunTraceFile)),
		)
	}

	return rows
}

func (m StatusModel) View() string {
	bold := lipgloss.NewStyle().Bold(true).Foreground(ColorNeon)
	dim := lipgloss.NewStyle().Foreground(ColorMuted)

	rows := StatusContentLines(m.info, m.ps, false)
	rows = append(rows, "", dim.Render("  press q to quit"))
	body := strings.Join(rows, "\n")

	return "\n" + lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(1, 3).
		Render(bold.Render("  OpsIntelligence Status")+"\n\n"+body) + "\n"
}

// ─────────────────────────────────────────────
// Commands
// ─────────────────────────────────────────────

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchPS(pid int) tea.Cmd {
	return func() tea.Msg {
		r := psResult{cpu: "0.0", rssKB: 0, vsz: "0", etime: "--:--", alive: false}

		out, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "%cpu,rss,vsz,etime", "--no-headers").Output()
		if err != nil {
			// macOS retry without --no-headers
			out, _ = exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "%cpu,rss,vsz,etime").Output()
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			// Skip header line that ps sometimes outputs on macOS
			if len(fields) >= 4 && fields[0] != "%CPU" {
				r.cpu = fields[0]
				if kb, e := strconv.ParseInt(fields[1], 10, 64); e == nil {
					r.rssKB = kb
				}
				r.vsz = fields[2]
				r.etime = fields[3]
				r.alive = true
				break
			}
		}
		return r
	}
}

// ─────────────────────────────────────────────
// Entry point
// ─────────────────────────────────────────────

