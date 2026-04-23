package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─────────────────────────────────────────────
// Public interfaces
// ─────────────────────────────────────────────

// RunResult mirrors agent.RunResult without creating an import cycle.
type RunResult struct {
	Iterations int
	Usage      struct{ TotalTokens int }
}

// AgentStreamHandler receives streaming events from the runner.
// Matches agent.StreamHandler exactly so the adapter in main.go is trivial.
type AgentStreamHandler interface {
	OnToken(token string)
	OnToolCall(name string, input json.RawMessage)
	OnToolResult(name, result string)
	OnDone(result *RunResult)
	OnError(err error)
}

// AgentRunner is the minimal surface the REPL needs from agent.Runner.
type AgentRunner interface {
	SessionID() string
	// RunStream dispatches the message and calls handler events as they arrive.
	// It must not block the caller; the TUI calls it from a goroutine.
	RunStream(ctx context.Context, msg string, handler AgentStreamHandler)
}

// ─────────────────────────────────────────────
// Internal Tea messages
// ─────────────────────────────────────────────

type agentTokenMsg string
type agentToolCallMsg struct{ name, snippet string }
type agentToolResultMsg struct{ name, snippet string }
type agentDoneMsg struct{ iterations, tokens int }
type agentErrMsg struct{ err error }
type pulseMsg struct{}

// ─────────────────────────────────────────────
// toolEvent — one tool call + its result
// ─────────────────────────────────────────────

type toolEvent struct {
	name    string
	input   string // short param snippet
	result  string // first line of result
	pending bool   // result not yet received
}

// ─────────────────────────────────────────────
// REPL model
// ─────────────────────────────────────────────

type REPLModel struct {
	ctx    context.Context
	cancel context.CancelFunc
	runner AgentRunner

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	// Chat history — rendered, ready for viewport
	history []string

	// Current streaming turn
	tokenBuf    string
	activeTools []toolEvent
	thinking    bool

	// Input recall (↑ key)
	sentMessages []string
	historyIdx   int // -1 = not in recall mode

	// UI dimensions / state
	width      int
	height     int
	ready      bool
	pulseFrame int
	sessionID  string
	version    string
	modelName  string // shown in footer

	sendMsg func(line string)
	banner  string
}

// NewREPLModel constructs the model. sendMsg is invoked on Enter; it must not block.
func NewREPLModel(
	ctx context.Context,
	runner AgentRunner,
	sessionID, ver, modelName string,
	providerCount, skillCount int,
	sendMsg func(string),
) *REPLModel {
	ctx, cancel := context.WithCancel(ctx)

	ta := textarea.New()
	ta.Placeholder = "Message OpsIntelligence…"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j")
	ta.CharLimit = 4096

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)

	return &REPLModel{
		ctx:        ctx,
		cancel:     cancel,
		runner:     runner,
		textarea:   ta,
		spinner:    sp,
		sendMsg:    sendMsg,
		sessionID:  sessionID,
		version:    ver,
		modelName:  modelName,
		historyIdx: -1,
		banner:     RenderBanner(ver, sessionID, providerCount, skillCount),
	}
}

func pulseCmd() tea.Cmd {
	return tea.Tick(480*time.Millisecond, func(time.Time) tea.Msg { return pulseMsg{} })
}

// ─────────────────────────────────────────────
// Init
// ─────────────────────────────────────────────

func (m *REPLModel) Init() tea.Cmd {
	// Seed the banner into history on first frame
	m.appendHistory(m.banner)
	return tea.Batch(textarea.Blink, m.spinner.Tick, pulseCmd())
}

// ─────────────────────────────────────────────
// Update
// ─────────────────────────────────────────────

func (m *REPLModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Resize ─────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := m.height - inputAreaHeight(m.height)
		if vpH < 4 {
			vpH = 4
		}
		if !m.ready {
			m.viewport = viewport.New(m.width-4, vpH)
			m.ready = true
		} else {
			m.viewport.Width = m.width - 4
			m.viewport.Height = vpH
		}
		m.textarea.SetWidth(m.width - 8)

	// ── Keyboard ───────────────────────────────
	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlC, tea.KeyEsc:
			m.cancel()
			return m, tea.Quit

		// Ctrl+L — clear history
		case tea.KeyCtrlL:
			m.history = nil
			m.tokenBuf = ""
			m.activeTools = nil
			m.refreshViewport()

		// Enter — send message
		case tea.KeyEnter:
			if !m.thinking {
				line := strings.TrimSpace(m.textarea.Value())
				if line != "" {
					m.sentMessages = append(m.sentMessages, line)
					m.historyIdx = -1
					m.appendHistory(renderUserMsg(line))
					m.appendHistory("")
					m.textarea.Reset()
					m.thinking = true
					m.tokenBuf = ""
					m.activeTools = nil
					m.refreshViewport()
					if m.sendMsg != nil {
						m.sendMsg(line)
					}
				}
			}

		// ↑ — recall previous message when textarea is empty
		case tea.KeyUp:
			if !m.thinking && m.textarea.Value() == "" && len(m.sentMessages) > 0 {
				if m.historyIdx == -1 {
					m.historyIdx = len(m.sentMessages) - 1
				} else if m.historyIdx > 0 {
					m.historyIdx--
				}
				m.textarea.SetValue(m.sentMessages[m.historyIdx])
				break
			}
			// Otherwise let viewport handle scroll
			var vc tea.Cmd
			m.viewport, vc = m.viewport.Update(msg)
			cmds = append(cmds, vc)

		// ↓ — forward through recall or scroll viewport
		case tea.KeyDown:
			if !m.thinking && m.historyIdx != -1 {
				if m.historyIdx < len(m.sentMessages)-1 {
					m.historyIdx++
					m.textarea.SetValue(m.sentMessages[m.historyIdx])
				} else {
					m.historyIdx = -1
					m.textarea.SetValue("")
				}
				break
			}
			var vc tea.Cmd
			m.viewport, vc = m.viewport.Update(msg)
			cmds = append(cmds, vc)
		}

	// ── Streaming events ───────────────────────
	case agentTokenMsg:
		m.tokenBuf += string(msg)
		m.refreshViewport()

	case agentToolCallMsg:
		// Flush any partial text before showing a tool call
		if m.tokenBuf != "" {
			m.flushToken()
		}
		m.activeTools = append(m.activeTools, toolEvent{
			name:    msg.name,
			input:   msg.snippet,
			pending: true,
		})
		m.refreshViewport()

	case agentToolResultMsg:
		// Mark the most-recent pending tool as done
		for i := len(m.activeTools) - 1; i >= 0; i-- {
			if m.activeTools[i].pending && m.activeTools[i].name == msg.name {
				m.activeTools[i].pending = false
				m.activeTools[i].result = msg.snippet
				break
			}
		}
		m.refreshViewport()

	case agentDoneMsg:
		m.flushToken()
		// Commit completed tool events to history
		for _, te := range m.activeTools {
			m.appendHistory(renderToolBlock(te))
		}
		m.activeTools = nil
		m.thinking = false
		m.appendHistory(Muted.Render(fmt.Sprintf(
			"   ▸ %d iter · %s tok", msg.iterations, fmtNum(msg.tokens),
		)))
		m.appendHistory("")
		m.refreshViewport()

	case agentErrMsg:
		m.flushToken()
		m.activeTools = nil
		m.thinking = false
		m.appendHistory(ErrorStyle.Render("✗ error: ") + Muted.Render(msg.err.Error()))
		m.appendHistory("")
		m.refreshViewport()

	case pulseMsg:
		m.pulseFrame++
		cmds = append(cmds, pulseCmd())

	case spinner.TickMsg:
		var sc tea.Cmd
		m.spinner, sc = m.spinner.Update(msg)
		cmds = append(cmds, sc)
	}

	// Always update textarea (when not thinking) and viewport
	if !m.thinking {
		var tc tea.Cmd
		m.textarea, tc = m.textarea.Update(msg)
		cmds = append(cmds, tc)
	}
	var vc tea.Cmd
	m.viewport, vc = m.viewport.Update(msg)
	cmds = append(cmds, vc)

	return m, tea.Batch(cmds...)
}

// ─────────────────────────────────────────────
// View
// ─────────────────────────────────────────────

func (m *REPLModel) View() string {
	if !m.ready {
		return "\n  " + Primary.Render("Starting OpsIntelligence…") + "\n"
	}

	borderCol := PulseBorder(m.pulseFrame)
	pulseMark := lipgloss.NewStyle().Foreground(borderCol).Bold(true).Render("▶")

	// ── Header bar ─────────────────────────────
	headerRow := lipgloss.JoinHorizontal(lipgloss.Left,
		pulseMark, " ",
		GradientWord("OPSINTELLIGENCE"), " ",
		CyberBracket("REPL"),
		Muted.Render("  "+strings.TrimSpace(m.version)+"  ·  "+shortID(m.sessionID)),
	)
	headerBar := lipgloss.NewStyle().Width(m.width - 2).Render(headerRow)
	under := lipgloss.NewStyle().Foreground(ColorBorder).Width(m.width - 2).
		Render(ScanlineSuffix(minReplScanlineWidth(m.width)))

	// ── Chat viewport ──────────────────────────
	m.viewport.SetContent(m.buildContent())
	chatBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderCol).
		Width(m.width - 2).
		Render(m.viewport.View())

	// ── Input + footer ─────────────────────────
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(ColorBorder).
		Width(m.width-2).
		Padding(0, 1).
		Render(Primary.Render("›") + " " + m.textarea.View() + "\n  " + m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, headerBar, under, chatBox, inputBox)
}

// renderFooter builds the context-sensitive hint line below the input.
func (m *REPLModel) renderFooter() string {
	if m.thinking {
		toolHint := ""
		for i := len(m.activeTools) - 1; i >= 0; i-- {
			te := m.activeTools[i]
			if te.pending {
				toolHint = "  " + ToolBadge.Render("⚡ "+te.name)
				if te.input != "" {
					toolHint += Muted.Render(" " + te.input)
				}
				break
			}
		}
		return m.spinner.View() + Neon.Render(" · ") + Muted.Render("thinking") + toolHint
	}

	model := ""
	if m.modelName != "" {
		model = Muted.Render("  ·  ") + Muted.Render(m.modelName)
	}
	return Muted.Render("↵ send  ·  ctrl+j newline  ·  ↑ recall  ·  ctrl+l clear  ·  esc quit") + model
}

// ─────────────────────────────────────────────
// Content builder
// ─────────────────────────────────────────────

func (m *REPLModel) buildContent() string {
	var sb strings.Builder

	// Committed history lines
	for _, l := range m.history {
		sb.WriteString(l + "\n")
	}

	// In-flight agent text (streaming)
	if m.tokenBuf != "" {
		sb.WriteString(AgentPrefix.Render("◈") + " " + renderMarkdown(m.tokenBuf))
	}

	// Active tool events (pending result)
	for _, te := range m.activeTools {
		if te.pending {
			sb.WriteString("\n" + renderToolPending(te.name, te.input, m.spinner.View()))
		} else {
			sb.WriteString("\n" + renderToolBlock(te))
		}
	}

	return sb.String()
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

func (m *REPLModel) appendHistory(line string) { m.history = append(m.history, line) }

func (m *REPLModel) flushToken() {
	if m.tokenBuf != "" {
		m.appendHistory(AgentPrefix.Render("◈") + " " + renderMarkdown(m.tokenBuf))
		m.tokenBuf = ""
	}
}

func (m *REPLModel) refreshViewport() {
	m.viewport.SetContent(m.buildContent())
	m.viewport.GotoBottom()
}

// inputAreaHeight returns how many rows to reserve for the input panel.
func inputAreaHeight(termH int) int {
	if termH < 20 {
		return 6
	}
	return 8
}

func minReplScanlineWidth(termW int) int {
	w := termW - 6
	if w < 24 {
		w = 24
	}
	if w > 72 {
		w = 72
	}
	return w
}

func fmtNum(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// ─────────────────────────────────────────────
// Message renderers
// ─────────────────────────────────────────────

func renderUserMsg(line string) string {
	return UserPrefix.Render("◉ You") + Muted.Render(" › ") + line
}

// renderToolPending shows an in-flight tool call with spinner.
func renderToolPending(name, input, spin string) string {
	label := ToolBadge.Render("  ╭─ " + name)
	if input != "" {
		label += Muted.Render(" " + input)
	}
	return label + " " + spin
}

// renderToolBlock shows a completed tool call with result.
func renderToolBlock(te toolEvent) string {
	top := ToolBadge.Render("  ╭─ " + te.name)
	if te.input != "" {
		top += Muted.Render(" " + te.input)
	}
	if te.result == "" {
		return top + "\n" + ToolBadge.Render("  ╰─ ") + Muted.Render("done")
	}
	result := te.result
	if len(result) > 120 {
		result = result[:120] + "…"
	}
	checkmark := lipgloss.NewStyle().Foreground(ColorCyan).Render("✓")
	return top + "\n" + ToolBadge.Render("  ╰─ ") + checkmark + " " + Muted.Render(result)
}

// ─────────────────────────────────────────────
// Markdown renderer (terminal-safe subset)
// ─────────────────────────────────────────────

var (
	codeBlockStyle = lipgloss.NewStyle().
			Foreground(ColorCyan).
			Background(ColorSurface).
			Padding(0, 1)
	codeLineStyle = lipgloss.NewStyle().
			Foreground(ColorNeon).
			Background(ColorSurface)
	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	boldRe       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// renderMarkdown converts a small subset of markdown to lipgloss-styled ANSI.
// Handles: fenced code blocks, inline code, **bold**, # headers.
func renderMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	inCode := false
	codeLang := ""

	for _, line := range lines {
		// Fenced code block start/end
		if strings.HasPrefix(line, "```") {
			if !inCode {
				inCode = true
				codeLang = strings.TrimPrefix(line, "```")
				hint := "code"
				if codeLang != "" {
					hint = codeLang
				}
				out = append(out, ToolBadge.Render("  ╭─ "+hint))
			} else {
				inCode = false
				codeLang = ""
				out = append(out, ToolBadge.Render("  ╰─────"))
			}
			continue
		}
		if inCode {
			out = append(out, codeLineStyle.Render("  │ "+line))
			continue
		}

		// H1/H2 headers
		if strings.HasPrefix(line, "## ") {
			out = append(out, Primary.Bold(true).Render(strings.TrimPrefix(line, "## ")))
			continue
		}
		if strings.HasPrefix(line, "# ") {
			out = append(out, Neon.Bold(true).Render(strings.TrimPrefix(line, "# ")))
			continue
		}

		// Inline transforms (bold, inline code)
		line = boldRe.ReplaceAllStringFunc(line, func(m string) string {
			inner := boldRe.FindStringSubmatch(m)[1]
			return lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render(inner)
		})
		line = inlineCodeRe.ReplaceAllStringFunc(line, func(m string) string {
			inner := inlineCodeRe.FindStringSubmatch(m)[1]
			return codeBlockStyle.Render(inner)
		})

		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ─────────────────────────────────────────────
// tuiStreamBridge — wires agent events → p.Send()
// ─────────────────────────────────────────────

type tuiStreamBridge struct {
	prog *tea.Program
}

func (b *tuiStreamBridge) OnToken(token string) {
	b.prog.Send(agentTokenMsg(token))
}

func (b *tuiStreamBridge) OnToolCall(name string, input json.RawMessage) {
	snippet := snippetFromJSON(input)
	b.prog.Send(agentToolCallMsg{name: name, snippet: snippet})
}

func (b *tuiStreamBridge) OnToolResult(name, result string) {
	snippet := firstLine(result, 120)
	b.prog.Send(agentToolResultMsg{name: name, snippet: snippet})
}

func (b *tuiStreamBridge) OnDone(r *RunResult) {
	iters, toks := 0, 0
	if r != nil {
		iters = r.Iterations
		toks = r.Usage.TotalTokens
	}
	b.prog.Send(agentDoneMsg{iterations: iters, tokens: toks})
}

func (b *tuiStreamBridge) OnError(err error) {
	b.prog.Send(agentErrMsg{err: err})
}

// snippetFromJSON extracts a compact param string from a tool input JSON object.
// E.g. {"path":"/tmp/foo","limit":100} → "path=/tmp/foo"
func snippetFromJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	priority := []string{"path", "command", "query", "url", "file", "repo", "owner", "name", "id"}
	for _, k := range priority {
		if v, ok := m[k]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:60] + "…"
			}
			return k + "=" + s
		}
	}
	// Fallback: first key
	for k, v := range m {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:60] + "…"
		}
		return k + "=" + s
	}
	return ""
}

// firstLine returns the first non-empty line of s, capped at max runes.
func firstLine(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rs := []rune(line)
		if len(rs) > max {
			return string(rs[:max]) + "…"
		}
		return line
	}
	return ""
}

// ─────────────────────────────────────────────
// RunREPL — entry point
// ─────────────────────────────────────────────

// RunREPL starts the bubbletea REPL.
// modelName is the active model string shown in the footer (pass "" to omit).
func RunREPL(ctx context.Context, runner AgentRunner, ver string, providerCount, skillCount int, modelName string) error {
	var p *tea.Program

	sendMsg := func(line string) {
		go func() {
			bridge := &tuiStreamBridge{prog: p}
			runner.RunStream(ctx, line, bridge)
		}()
	}

	model := NewREPLModel(ctx, runner, runner.SessionID(), ver, modelName, providerCount, skillCount, sendMsg)
	p = tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	_, err := p.Run()
	return err
}
