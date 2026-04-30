package tui

// onboard_model.go — Bubbletea shell for the onboarding wizard.
//
// All huh forms run inside one alt-screen bubbletea program so the
// progress header is always anchored at the top.  Side-effect steps
// (downloads, docker, config save) show a spinner while running in
// the background.
//
// Usage:
//
//	steps := tui.BuildOnboardSteps(...)
//	if err := tui.RunOnboardWizard(ctx, steps); err != nil { … }

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ── Step definition ───────────────────────────────────────────────────────

// OnboardWizardStep defines one step in the onboarding wizard.
// Either MakeForm OR SideEffect must be non-nil (not both).
//
//   - Form steps display a huh form inside the persistent chrome.
//   - Side-effect steps display a spinner and run fn in the background.
//
// Condition, when non-nil, is evaluated right before the step becomes
// active.  Returning false skips the step without user interaction.
type OnboardWizardStep struct {
	// ── identity (shown in header) ──────────────────────────────────
	Icon     string
	Title    string
	Subtitle string

	// ── form step ───────────────────────────────────────────────────
	MakeForm func() *huh.Form

	// ── side-effect step ────────────────────────────────────────────
	SideEffect      func() error
	SideEffectLabel string

	// ── conditional skip ────────────────────────────────────────────
	// When non-nil and returns false the step is silently skipped.
	Condition func() bool
}

// OnboardFormStep builds a form-type step.
func OnboardFormStep(icon, title, subtitle string, mk func() *huh.Form) OnboardWizardStep {
	return OnboardWizardStep{Icon: icon, Title: title, Subtitle: subtitle, MakeForm: mk}
}

// OnboardSideStep builds a side-effect step.
func OnboardSideStep(label string, fn func() error) OnboardWizardStep {
	return OnboardWizardStep{SideEffectLabel: label, SideEffect: fn}
}

// OnboardConditionalFormStep builds a conditionally-shown form step.
func OnboardConditionalFormStep(icon, title, subtitle string, cond func() bool, mk func() *huh.Form) OnboardWizardStep {
	return OnboardWizardStep{Icon: icon, Title: title, Subtitle: subtitle, Condition: cond, MakeForm: mk}
}

// OnboardConditionalSideStep builds a conditionally-run side-effect step.
func OnboardConditionalSideStep(label string, cond func() bool, fn func() error) OnboardWizardStep {
	return OnboardWizardStep{SideEffectLabel: label, Condition: cond, SideEffect: fn}
}

// ── Entry point ───────────────────────────────────────────────────────────

// RunOnboardWizard drives all steps inside a single bubbletea alt-screen.
// Returns nil on completion or context cancellation; returns an error
// only for bubbletea infrastructure failures.
func RunOnboardWizard(ctx context.Context, steps []OnboardWizardStep) error {
	m := newOnboardWizardModel(ctx, steps)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	)
	_, err := p.Run()
	return err
}

// ── bubbletea model ───────────────────────────────────────────────────────

type onboardWizardModel struct {
	ctx   context.Context
	steps []OnboardWizardStep
	idx   int

	form    *huh.Form
	spinner spinner.Model
	width   int
	height  int

	// side-effect
	running   bool
	sideLabel string
	sideErr   error

	// counts (computed once at init)
	totalFormSteps int
}

type onboardSideDoneMsg struct{ err error }

func newOnboardWizardModel(ctx context.Context, steps []OnboardWizardStep) *onboardWizardModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	total := 0
	for _, s := range steps {
		if s.MakeForm != nil {
			total++
		}
	}
	return &onboardWizardModel{ctx: ctx, steps: steps, spinner: sp, totalFormSteps: total}
}

// formIndexAt returns the 1-based form-step number for steps[idx].
func (m *onboardWizardModel) formIndexAt(idx int) int {
	n := 0
	for i := 0; i <= idx && i < len(m.steps); i++ {
		if m.steps[i].MakeForm != nil {
			n++
		}
	}
	return n
}

// ── Init ──────────────────────────────────────────────────────────────────

func (m *onboardWizardModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.activateStep())
}

// activateStep advances past any skipped steps and activates m.idx.
func (m *onboardWizardModel) activateStep() tea.Cmd {
	// Skip conditional steps that are inactive.
	for m.idx < len(m.steps) {
		s := &m.steps[m.idx]
		if s.Condition != nil && !s.Condition() {
			m.idx++
			continue
		}
		break
	}
	if m.idx >= len(m.steps) {
		return tea.Quit
	}
	s := &m.steps[m.idx]
	if s.MakeForm != nil {
		m.running = false
		m.form = s.MakeForm().WithTheme(OnboardTheme())
		return m.form.Init()
	}
	// side-effect step
	m.running = true
	m.sideLabel = s.SideEffectLabel
	m.sideErr = nil
	fn := s.SideEffect
	return func() tea.Msg {
		return onboardSideDoneMsg{err: fn()}
	}
}

// ── Update ────────────────────────────────────────────────────────────────

func (m *onboardWizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

	case onboardSideDoneMsg:
		m.running = false
		m.sideErr = msg.err
		m.idx++
		return m, m.activateStep()

	case spinner.TickMsg:
		var sc tea.Cmd
		m.spinner, sc = m.spinner.Update(msg)
		cmds = append(cmds, sc)
	}

	// Delegate to the active form.
	if !m.running && m.form != nil {
		f, fc := m.form.Update(msg)
		m.form = f.(*huh.Form)
		cmds = append(cmds, fc)
		if m.form.State == huh.StateCompleted || m.form.State == huh.StateAborted {
			m.idx++
			// Clear the completed form BEFORE activateStep sets the next one,
			// so the assignment in activateStep is not immediately overwritten.
			m.form = nil
			initCmd := m.activateStep()
			cmds = append(cmds, initCmd)
			// Forward current terminal dimensions to the freshly created form so
			// it renders immediately without waiting for a resize event.
			if !m.running && m.form != nil {
				w, h := m.width, m.height
				cmds = append(cmds, func() tea.Msg {
					return tea.WindowSizeMsg{Width: w, Height: h}
				})
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// ── View ──────────────────────────────────────────────────────────────────

func (m *onboardWizardModel) View() string {
	if m.width == 0 {
		return lipgloss.Place(80, 24, lipgloss.Left, lipgloss.Top,
			lipgloss.NewStyle().Foreground(ColorMuted).Padding(1, 2).Render("\n  Loading…\n"),
		)
	}
	var inner string
	if m.idx >= len(m.steps) {
		inner = m.viewDone()
	} else {
		inner = m.viewChrome()
	}
	// Use the terminal's own background everywhere — no background fill.
	// Text colours, borders and indicators provide all the visual structure.
	padded := lipgloss.NewStyle().
		Foreground(ColorOnSurface).
		Padding(1, 2).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, padded)
}

func (m *onboardWizardModel) viewChrome() string {
	w := m.width - 4
	if w < 50 {
		w = 50
	}
	header := m.renderHeader(w)
	body := m.renderBody()
	return header + "\n" + body
}

// renderHeader is the persistent top strip that never scrolls away.
//
//	OPSINTELLIGENCE  ████░░░░░░  3 / 10  🧠  AI Provider
func (m *onboardWizardModel) renderHeader(w int) string {
	formIdx := m.formIndexAt(m.idx)
	total := m.totalFormSteps

	// Progress bar
	pct := 0.0
	if total > 0 {
		pct = float64(formIdx-1) / float64(total) * 100
	}
	bar := ProgressBarLavender(pct, 18)

	// Step pill
	pill := lipgloss.NewStyle().
		Background(ColorPrimary).
		Foreground(ColorOnAccent).
		Bold(true).
		Padding(0, 1).
		Render(fmt.Sprintf("%d / %d", formIdx, total))

	// Active step title
	s := &m.steps[m.idx]
	stepTitle := ""
	if s.Title != "" {
		stepTitle = lipgloss.NewStyle().Foreground(ColorNeon).Bold(true).
			Render(s.Icon + "  " + s.Title)
	} else if m.running && m.sideLabel != "" {
		stepTitle = lipgloss.NewStyle().Foreground(ColorMuted).Render(m.sideLabel)
	}

	// Main row: brand  bar  pill  title
	left := GradientWord("OPSINTELLIGENCE") + "  " + bar + "  " + pill
	if stepTitle != "" {
		left += "  " + stepTitle
	}
	topRow := lipgloss.NewStyle().Width(w).Render(left)

	// Subtitle
	subRow := ""
	if s.Subtitle != "" {
		subRow = "\n" + lipgloss.NewStyle().Foreground(ColorMuted).PaddingLeft(4).
			Render(s.Subtitle)
	}

	// Divider
	divLen := w
	if ll := lipgloss.Width(topRow); ll > divLen {
		divLen = ll
	}
	div := "\n" + lipgloss.NewStyle().Foreground(ColorBorder).
		Render(strings.Repeat("╌", divLen))

	return topRow + subRow + div
}

func (m *onboardWizardModel) renderBody() string {
	if m.running {
		label := lipgloss.NewStyle().Foreground(ColorMuted).Render(m.sideLabel + "…")
		line := "\n  " + m.spinner.View() + "  " + label + "\n"
		if m.sideErr != nil {
			line += "\n  " + lipgloss.NewStyle().Foreground(ColorError).
				Render("✗  "+m.sideErr.Error()) + "\n"
		}
		return line
	}
	if m.form != nil {
		return m.form.View()
	}
	return ""
}

func (m *onboardWizardModel) viewDone() string {
	check := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("  ✓")
	text := lipgloss.NewStyle().Foreground(ColorOnSurface).Render("  Setup complete")
	return "\n" + check + text + "\n"
}
