package tui

// setup.go — guided Gemma + MemPalace quick-start wizard.
//
// Full bubbletea alt-screen wizard: welcome → options → progress → done.
// No raw fmt.Print / ANSI escape hacks.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ─────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────

// SetupOptions are provided by the caller (main.go / quickstart command).
type SetupOptions struct {
	StateDir        string // e.g. ~/.opsintelligence
	GGUFPath        string // override; empty = auto
	BootstrapPython string // override; empty = "python3"
	Version         string
}

// SetupResult is returned when the wizard completes.
type SetupResult struct {
	MemPalaceEnabled bool
	GemmaEnabled     bool
	GGUFPath         string
	YAMLSnippet      string // ready-to-paste config block
}

// ─────────────────────────────────────────────
// RunSetupWizard — entry point
// ─────────────────────────────────────────────

// RunSetupWizard runs the interactive setup wizard as a full-screen bubbletea program.
func RunSetupWizard(ctx context.Context, opts SetupOptions) (*SetupResult, error) {
	m := newWizardModel(ctx, opts)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	wm, ok := final.(*wizardModel)
	if !ok || wm.result == nil {
		return &SetupResult{}, nil
	}

	// Print YAML snippet in normal terminal after alt-screen exits.
	if wm.result.YAMLSnippet != "" {
		printYAMLSnippet(wm.result.YAMLSnippet)
	}
	return wm.result, nil
}

// ─────────────────────────────────────────────
// Wizard steps
// ─────────────────────────────────────────────

type wizardStep int

const (
	stepWelcome wizardStep = iota // static welcome screen
	stepOptions                   // huh form — choose components
	stepRunning                   // spinner progress per task
	stepDone                      // results summary
)

// ─────────────────────────────────────────────
// wizardModel — root bubbletea model
// ─────────────────────────────────────────────

type wizardTask struct {
	label string
	fn    func() error
}

type wizardTaskResult struct {
	label string
	err   error
}

type wizardTaskDoneMsg struct{ err error }
type wizardPulseMsg struct{}

type wizardModel struct {
	ctx  context.Context
	opts SetupOptions
	step wizardStep

	width  int
	height int
	pulse  int

	// stepOptions
	form      *huh.Form
	wantMem   bool
	wantGemma bool
	pyAvail   bool

	// stepRunning
	spinner   spinner.Model
	tasks     []wizardTask
	taskIdx   int
	taskDone  chan error
	completed []wizardTaskResult

	// stepDone
	result *SetupResult
}

func newWizardModel(ctx context.Context, opts SetupOptions) *wizardModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)

	return &wizardModel{
		ctx:     ctx,
		opts:    opts,
		step:    stepWelcome,
		spinner: sp,
		pyAvail: pythonAvailable(opts.BootstrapPython),
	}
}

func wizardPulseCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return wizardPulseMsg{} })
}

// ─────────────────────────────────────────────
// Init
// ─────────────────────────────────────────────

func (m *wizardModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, wizardPulseCmd())
}

// ─────────────────────────────────────────────
// Update
// ─────────────────────────────────────────────

func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.step == stepWelcome {
				return m, m.initOptions()
			}
			if m.step == stepDone {
				return m, tea.Quit
			}
		case tea.KeyEsc:
			if m.step == stepDone {
				return m, tea.Quit
			}
		}

	case wizardPulseMsg:
		m.pulse++
		cmds = append(cmds, wizardPulseCmd())

	case spinner.TickMsg:
		var sc tea.Cmd
		m.spinner, sc = m.spinner.Update(msg)
		cmds = append(cmds, sc)

	case wizardTaskDoneMsg:
		m.completed = append(m.completed, wizardTaskResult{
			label: m.tasks[m.taskIdx].label,
			err:   msg.err,
		})
		m.taskIdx++
		if m.taskIdx < len(m.tasks) {
			cmds = append(cmds, m.launchTask(m.taskIdx))
		} else {
			m.result = m.buildResult()
			m.step = stepDone
		}
	}

	// Delegate key events to huh form during stepOptions.
	if m.step == stepOptions && m.form != nil {
		f, fc := m.form.Update(msg)
		m.form = f.(*huh.Form)
		cmds = append(cmds, fc)
		if m.form.State == huh.StateCompleted {
			return m, m.startTasks()
		}
		if m.form.State == huh.StateAborted {
			return m, tea.Quit
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *wizardModel) initOptions() tea.Cmd {
	m.step = stepOptions
	pyHint := ""
	if !m.pyAvail {
		pyHint = " (python3 not found)"
	}

	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Choose Components").
				Description(
					"  MemPalace  — hierarchical memory for your agent\n" +
						"  Gemma      — on-device LLM for fast routing (~3 GiB)\n",
				),
			huh.NewConfirm().
				Title("Set up MemPalace?"+pyHint).
				Value(&m.wantMem).
				Affirmative("Yes").
				Negative("Skip"),
			huh.NewConfirm().
				Title("Download Gemma (local AI routing)?").
				Value(&m.wantGemma).
				Affirmative("Yes").
				Negative("Skip"),
		),
	).WithTheme(setupTheme())

	return m.form.Init()
}

func (m *wizardModel) startTasks() tea.Cmd {
	m.tasks = m.buildTasks()
	if len(m.tasks) == 0 {
		m.result = m.buildResult()
		m.step = stepDone
		return nil
	}
	m.step = stepRunning
	m.taskIdx = 0
	return m.launchTask(0)
}

func (m *wizardModel) launchTask(idx int) tea.Cmd {
	task := m.tasks[idx]
	ch := make(chan error, 1)
	m.taskDone = ch
	go func() { ch <- task.fn() }()
	return func() tea.Msg {
		return wizardTaskDoneMsg{err: <-ch}
	}
}

func (m *wizardModel) buildTasks() []wizardTask {
	var tasks []wizardTask

	if m.wantMem && m.pyAvail {
		tasks = append(tasks, wizardTask{
			label: "Installing MemPalace",
			fn:    func() error { return runMemPalaceSetup(m.ctx, m.opts) },
		})
	}

	if m.wantGemma {
		ggufDest := m.opts.GGUFPath
		if ggufDest == "" {
			ggufDest = filepath.Join(m.opts.StateDir, "models", "gemma-4-e2b-it.gguf")
		}
		if _, err := os.Stat(ggufDest); err != nil {
			tasks = append(tasks, wizardTask{
				label: "Downloading Gemma GGUF",
				fn:    func() error { return runGemmaSetup(m.ctx, ggufDest) },
			})
		}
	}

	return tasks
}

func (m *wizardModel) buildResult() *SetupResult {
	r := &SetupResult{}
	ggufDest := m.opts.GGUFPath
	if ggufDest == "" {
		ggufDest = filepath.Join(m.opts.StateDir, "models", "gemma-4-e2b-it.gguf")
	}
	for _, tr := range m.completed {
		if tr.err != nil {
			continue
		}
		switch {
		case strings.Contains(tr.label, "MemPalace"):
			r.MemPalaceEnabled = true
		case strings.Contains(tr.label, "Gemma"):
			r.GemmaEnabled = true
			r.GGUFPath = ggufDest
		}
	}
	// If user said yes but skipped because already present.
	if m.wantGemma {
		if _, err := os.Stat(ggufDest); err == nil {
			r.GemmaEnabled = true
			r.GGUFPath = ggufDest
		}
	}
	r.YAMLSnippet = buildYAMLSnippet(r, m.opts)
	return r
}

// ─────────────────────────────────────────────
// View
// ─────────────────────────────────────────────

func (m *wizardModel) View() string {
	if m.width == 0 {
		return "\n  Loading…\n"
	}
	inner := m.viewBody()
	return m.viewChrome(inner)
}

func (m *wizardModel) viewChrome(body string) string {
	w := m.width - 4
	if w < 40 {
		w = 40
	}

	// ── Header ──────────────────────────────────
	stepLabel := m.stepLabel()
	left := lipgloss.JoinHorizontal(lipgloss.Left,
		ChromePrompt.Render("›"), " ",
		GradientWord("OPSINTELLIGENCE"), " ",
		Muted.Render("setup"),
	)
	right := Muted.Render(stepLabel)
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	headerRow := left + strings.Repeat(" ", gap) + right
	header := lipgloss.NewStyle().Width(w).Render(headerRow)
	under := lipgloss.NewStyle().Foreground(ColorBorder).Width(w).
		Render(ScanlineSuffix(minReplScanlineWidth(w)))

	// ── Body box ────────────────────────────────
	borderCol := PulseBorder(m.pulse)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderCol).
		Width(w).
		Padding(1, 2).
		Render(body)

	// ── Footer hint ─────────────────────────────
	hint := m.footerHint()
	footer := Muted.Render(hint)

	return lipgloss.JoinVertical(lipgloss.Left, header, under, box, footer)
}

func (m *wizardModel) stepLabel() string {
	switch m.step {
	case stepWelcome:
		return "Step 1/3 · Welcome"
	case stepOptions:
		return "Step 2/3 · Configure"
	case stepRunning:
		return "Step 3/3 · Installing"
	case stepDone:
		return "Done"
	}
	return ""
}

func (m *wizardModel) footerHint() string {
	switch m.step {
	case stepWelcome:
		return "  enter to continue  ·  ctrl+c to quit"
	case stepOptions:
		return "  ↑↓ navigate  ·  enter/space select  ·  ctrl+c quit"
	case stepRunning:
		return "  installing…  ·  ctrl+c to abort"
	case stepDone:
		return "  enter to exit  ·  YAML snippet printed after"
	}
	return ""
}

func (m *wizardModel) viewBody() string {
	switch m.step {
	case stepWelcome:
		return m.viewWelcome()
	case stepOptions:
		return m.viewOptions()
	case stepRunning:
		return m.viewRunning()
	case stepDone:
		return m.viewDone()
	}
	return ""
}

func (m *wizardModel) viewWelcome() string {
	w := m.width - 12
	if w < 36 {
		w = 36
	}

	title := lipgloss.NewStyle().
		Foreground(ColorNeon).Bold(true).Width(w).Align(lipgloss.Center).
		Render("Smart Mode Setup")

	ver := ""
	if m.opts.Version != "" {
		ver = "\n" + lipgloss.NewStyle().Width(w).Align(lipgloss.Center).
			Render(Muted.Render(m.opts.Version))
	}

	desc := lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(
		Muted.Render("This wizard sets up two optional components:"),
	)

	comp1 := Primary.Render("  MemPalace") + Muted.Render("  — hierarchical agent memory")
	comp2 := Primary.Render("  Gemma    ") + Muted.Render("  — on-device LLM routing (~3 GiB)")

	note := lipgloss.NewStyle().Width(w).Align(lipgloss.Center).
		Render(Muted.Render("Takes ~2 min · internet needed for downloads"))

	cta := lipgloss.NewStyle().
		Foreground(ColorPrimary).Bold(true).Width(w).Align(lipgloss.Center).
		Render("[ Press Enter to continue ]")

	return strings.Join([]string{
		title, ver, "",
		desc, "",
		comp1, comp2, "",
		note, "",
		cta,
	}, "\n")
}

func (m *wizardModel) viewOptions() string {
	if m.form == nil {
		return Muted.Render("  Loading form…")
	}
	return m.form.View()
}

func (m *wizardModel) viewRunning() string {
	var sb strings.Builder

	// Completed tasks
	for _, tr := range m.completed {
		if tr.err != nil {
			sb.WriteString(ErrorStyle.Render("  ✗ ") + tr.label + Muted.Render(" — "+tr.err.Error()) + "\n")
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(ColorCyan).Render("  ✓ ") + tr.label + "\n")
		}
	}

	// Current in-flight task
	if m.taskIdx < len(m.tasks) {
		cur := m.tasks[m.taskIdx]
		sb.WriteString("  " + m.spinner.View() + " " + Muted.Render(cur.label+"…") + "\n")
	}

	return sb.String()
}

func (m *wizardModel) viewDone() string {
	if m.result == nil {
		return Muted.Render("  Nothing was configured.")
	}
	var sb strings.Builder
	sb.WriteString(Neon.Bold(true).Render("  Setup complete") + "\n\n")

	for _, tr := range m.completed {
		if tr.err != nil {
			sb.WriteString(ErrorStyle.Render("  ✗ ") + tr.label + Muted.Render(": "+tr.err.Error()) + "\n")
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(ColorCyan).Render("  ✓ ") + tr.label + "\n")
		}
	}
	if len(m.completed) == 0 {
		sb.WriteString(Muted.Render("  Nothing was installed.") + "\n")
	}

	if m.result.YAMLSnippet != "" {
		sb.WriteString("\n" + Muted.Render("  YAML config snippet will be shown after exit.") + "\n")
	}

	sb.WriteString("\n" + Muted.Render("  Run  opsintelligence doctor  to verify."))
	return sb.String()
}

// ─────────────────────────────────────────────
// Exported helpers (used by onboard.go, etc.)
// ─────────────────────────────────────────────

// RunWithSpinner runs fn in the background, displaying a bubbletea spinner.
func RunWithSpinner(ctx context.Context, label string, fn func() error) (bool, error) {
	pm := newProgressModel(label)
	go func() { pm.result <- fn() }()
	p := tea.NewProgram(pm, tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return false, err
	}
	fm := final.(progressModel)
	return fm.err == nil, fm.err
}

// RunMemPalaceSetup is the exported variant so onboard.go can reuse the logic.
func RunMemPalaceSetup(ctx context.Context, opts SetupOptions) error {
	return runMemPalaceSetup(ctx, opts)
}

// ─────────────────────────────────────────────
// Setup sub-routines (shell-out)
// ─────────────────────────────────────────────

func runMemPalaceSetup(ctx context.Context, opts SetupOptions) error {
	py := opts.BootstrapPython
	if py == "" {
		py = "python3"
	}
	venvRoot := filepath.Join(opts.StateDir, "mempalace", "venv")
	world := filepath.Join(opts.StateDir, "mempalace", "world")

	if err := os.MkdirAll(venvRoot, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(venvRoot, pythonBin())); err != nil {
		if err := shellRun(ctx, py, "-m", "venv", venvRoot); err != nil {
			return fmt.Errorf("create venv: %w", err)
		}
	}
	vpy := filepath.Join(venvRoot, pythonBin())
	if err := shellRun(ctx, vpy, "-c", "import mempalace"); err != nil {
		if err := shellRun(ctx, vpy, "-m", "pip", "install", "-q", "-U", "mempalace"); err != nil {
			return fmt.Errorf("pip install: %w", err)
		}
	}
	marker := filepath.Join(opts.StateDir, "mempalace", ".world_initialized")
	if _, err := os.Stat(marker); err != nil {
		if err := os.MkdirAll(world, 0o755); err != nil {
			return err
		}
		cli := filepath.Join(venvRoot, "bin", "mempalace")
		if runtime.GOOS == "windows" {
			cli = filepath.Join(venvRoot, "Scripts", "mempalace.exe")
		}
		initErr := shellRun(ctx, cli, "init", world, "--yes")
		if initErr != nil {
			initErr = shellRun(ctx, vpy, "-m", "mempalace", "init", world, "--yes")
		}
		if initErr != nil {
			return fmt.Errorf("mempalace init: %w", initErr)
		}
		_ = os.WriteFile(marker, []byte("1\n"), 0o644)
	}
	return nil
}

func runGemmaSetup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "opsintelligence"
	}
	return shellRun(ctx, exe, "local-intel", "setup", "--gguf-path", dest)
}

func shellRun(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func pythonBin() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("Scripts", "python.exe")
	}
	return filepath.Join("bin", "python3")
}

func pythonAvailable(override string) bool {
	py := override
	if py == "" {
		py = "python3"
	}
	_, err := exec.LookPath(py)
	return err == nil
}

// ─────────────────────────────────────────────
// YAML snippet builder + printer
// ─────────────────────────────────────────────

func buildYAMLSnippet(r *SetupResult, opts SetupOptions) string {
	if !r.MemPalaceEnabled && !r.GemmaEnabled {
		return ""
	}
	var sb strings.Builder
	if r.MemPalaceEnabled {
		sb.WriteString("memory:\n")
		sb.WriteString("  mempalace:\n")
		sb.WriteString("    enabled: true\n")
		sb.WriteString("    auto_start: true\n")
		sb.WriteString("    managed_venv: true\n")
		sb.WriteString("    inject_into_memory_search: false\n")
	}
	if r.GemmaEnabled {
		sb.WriteString("agent:\n")
		sb.WriteString("  local_intel:\n")
		sb.WriteString("    enabled: true\n")
		sb.WriteString(fmt.Sprintf("    gguf_path: %q\n", r.GGUFPath))
		sb.WriteString("    max_tokens: 256\n")
		sb.WriteString(fmt.Sprintf("    cache_dir: %q\n", filepath.Join(opts.StateDir, "localintel")))
		sb.WriteString("    smart_routing: true\n")
	}
	return sb.String()
}

func printYAMLSnippet(snippet string) {
	fmt.Println()
	fmt.Println(Primary.Bold(true).Render("▸ Add to opsintelligence.yaml:"))
	fmt.Println(Muted.Render("  (merge with existing memory:/agent: blocks if present)"))
	fmt.Println()
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(0, 2).
		Foreground(ColorCyan)
	fmt.Println(border.Render(snippet))
	fmt.Println(Muted.Render("  Config location: ~/.opsintelligence/opsintelligence.yaml"))
	fmt.Println(Muted.Render("  Re-run  opsintelligence doctor  to verify."))
	fmt.Println()
}

// ─────────────────────────────────────────────
// UI helpers
// ─────────────────────────────────────────────

// setupTheme returns a huh theme consistent with the OpsIntelligence palette.
func setupTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Title = t.Focused.Title.Foreground(ColorNeon).Bold(true)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(ColorPrimary).Bold(true)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(ColorMuted)
	t.Focused.Description = t.Focused.Description.Foreground(ColorMuted)
	t.Blurred.Title = t.Blurred.Title.Foreground(ColorMuted)
	return t
}

// spinnerView renders a one-frame spinner string without a bubbletea model.
func spinnerView(frame int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render(frames[frame%len(frames)])
}

// ─────────────────────────────────────────────
// progressModel — standalone spinner bubbletea model
// (used by RunWithSpinner for single-task progress display)
// ─────────────────────────────────────────────

type progressModel struct {
	spinner spinner.Model
	label   string
	done    bool
	err     error
	result  chan error
}

func newProgressModel(label string) progressModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	return progressModel{spinner: sp, label: label, result: make(chan error, 1)}
}

type progressDoneMsg struct{ err error }

func (m progressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		err := <-m.result
		return progressDoneMsg{err: err}
	})
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressDoneMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m progressModel) View() string {
	if m.done {
		if m.err != nil {
			return ErrorStyle.Render("  ✗ ") + Muted.Render(m.err.Error()) + "\n"
		}
		return lipgloss.NewStyle().Foreground(ColorCyan).Render("  ✓ ") + m.label + "\n"
	}
	return "  " + m.spinner.View() + " " + Muted.Render(m.label+"…") + "\n"
}
