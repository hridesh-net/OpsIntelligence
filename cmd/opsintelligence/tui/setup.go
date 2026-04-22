package tui

// setup.go — guided Gemma + MemPalace quick-start wizard.
//
// Designed for first-time / naive users who want smart local AI routing
// (Gemma) and structured memory (MemPalace) without editing YAML by hand.
//
// Entry point: RunSetupWizard()

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
// Setup wizard state
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

// RunSetupWizard runs the interactive Gemma + MemPalace setup wizard.
// It uses huh forms for selection and a bubbletea spinner for progress tasks.
func RunSetupWizard(ctx context.Context, opts SetupOptions) (*SetupResult, error) {
	fmt.Print(renderSetupHeader(opts.Version))

	result := &SetupResult{}

	// ── Step 1: What to set up ─────────────────
	var wantMemPalace, wantGemma bool

	pyAvail := pythonAvailable(opts.BootstrapPython)
	pyHint := ""
	if !pyAvail {
		pyHint = " (python3 not found — MemPalace requires Python 3.9+)"
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Smart Mode Setup").
				Description(
					"This wizard installs two optional components:\n\n"+
						"  • MemPalace  — hierarchical memory for your agent\n"+
						"  • Gemma      — on-device LLM for fast routing\n\n"+
						"Both are optional. You can set up one or both.",
				),
			huh.NewConfirm().
				Title("Set up MemPalace (Python memory system)?"+pyHint).
				Value(&wantMemPalace).
				Affirmative("Yes").
				Negative("Skip"),
			huh.NewConfirm().
				Title("Set up Gemma (local AI routing, ~3 GiB download)?").
				Value(&wantGemma).
				Affirmative("Yes").
				Negative("Skip"),
		),
	).WithTheme(setupTheme())

	if err := form.RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("setup wizard cancelled: %w", err)
	}

	if !wantMemPalace && !wantGemma {
		fmt.Println(Muted.Render("\nNothing selected — skipping setup."))
		return result, nil
	}

	// ── Step 2: MemPalace ──────────────────────
	if wantMemPalace {
		if !pyAvail {
			fmt.Println(ErrorStyle.Render("\n✗ python3 not found — skipping MemPalace."))
			fmt.Println(Muted.Render("  Install Python 3.9+ and re-run: opsintelligence quickstart"))
		} else {
			fmt.Println(Primary.Render("\n▸ Setting up MemPalace…"))
			ok, err := runWithSpinner(ctx, "Installing MemPalace", func() error {
				return runMemPalaceSetup(ctx, opts)
			})
			if err != nil || !ok {
				fmt.Println(ErrorStyle.Render("  ✗ MemPalace setup failed: ") + Muted.Render(fmt.Sprintf("%v", err)))
				fmt.Println(Muted.Render("  Try manually: opsintelligence mempalace setup"))
			} else {
				fmt.Println(lipgloss.NewStyle().Foreground(ColorCyan).Render("  ✓ MemPalace ready"))
				result.MemPalaceEnabled = true
			}
		}
	}

	// ── Step 3: Gemma ──────────────────────────
	if wantGemma {
		ggufDest := opts.GGUFPath
		if ggufDest == "" {
			ggufDest = filepath.Join(opts.StateDir, "models", "gemma-4-e2b-it.gguf")
		}

		// Check if already present
		if _, err := os.Stat(ggufDest); err == nil {
			fmt.Println(Primary.Render("\n▸ Gemma GGUF already present at " + ggufDest))
			result.GemmaEnabled = true
			result.GGUFPath = ggufDest
		} else {
			fmt.Println(Primary.Render("\n▸ Setting up Gemma…"))
			fmt.Println(Muted.Render("  Destination: " + ggufDest))
			fmt.Println(Muted.Render("  Size: ~3 GiB — this may take several minutes on a slow connection."))
			fmt.Println(Muted.Render("  You can run this in the background: opsintelligence local-intel setup"))
			fmt.Println()

			var doDownload bool
			confirmForm := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title("Download Gemma GGUF now?").
						Description("Alternatively, place the GGUF at:\n  " + ggufDest).
						Value(&doDownload).
						Affirmative("Download").
						Negative("Skip"),
				),
			).WithTheme(setupTheme())

			if err := confirmForm.RunWithContext(ctx); err == nil && doDownload {
				ok, dlErr := runWithSpinner(ctx, "Downloading Gemma GGUF", func() error {
					return runGemmaSetup(ctx, ggufDest)
				})
				if dlErr != nil || !ok {
					fmt.Println(ErrorStyle.Render("  ✗ Gemma download failed: ") + Muted.Render(fmt.Sprintf("%v", dlErr)))
					fmt.Println(Muted.Render("  Try manually: opsintelligence local-intel setup"))
				} else {
					fmt.Println(lipgloss.NewStyle().Foreground(ColorCyan).Render("  ✓ Gemma ready"))
					result.GemmaEnabled = true
					result.GGUFPath = ggufDest
				}
			}
		}
	}

	// ── Step 4: YAML snippet ───────────────────
	result.YAMLSnippet = buildYAMLSnippet(result, opts)
	if result.YAMLSnippet != "" {
		printYAMLSnippet(result.YAMLSnippet)
	}

	return result, nil
}

// ─────────────────────────────────────────────
// Spinner progress helper
// ─────────────────────────────────────────────

type spinnerDoneMsg struct{ err error }

func runWithSpinner(ctx context.Context, label string, fn func() error) (bool, error) {
	type result struct {
		err error
	}

	ch := make(chan result, 1)
	go func() {
		ch <- result{err: fn()}
	}()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)

	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case res := <-ch:
			fmt.Print("\r\033[K") // clear spinner line
			return res.err == nil, res.err
		case <-tick.C:
			sp.Spinner = nextDot(sp.Spinner)
			fmt.Printf("\r  %s %s", sp.View(), Muted.Render(label+"…"))
		}
	}
}

// nextDot cycles the spinner dot manually (no bubbletea event loop needed).
func nextDot(s spinner.Spinner) spinner.Spinner { return s }

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

	// Create venv
	if _, err := os.Stat(filepath.Join(venvRoot, pythonBin())); err != nil {
		if err := shellRun(ctx, py, "-m", "venv", venvRoot); err != nil {
			return fmt.Errorf("create venv: %w", err)
		}
	}

	vpy := filepath.Join(venvRoot, pythonBin())

	// Install mempalace
	if err := shellRun(ctx, vpy, "-c", "import mempalace"); err != nil {
		if err := shellRun(ctx, vpy, "-m", "pip", "install", "-q", "-U", "mempalace"); err != nil {
			return fmt.Errorf("pip install: %w", err)
		}
	}

	// Init world (idempotent via marker)
	marker := filepath.Join(opts.StateDir, "mempalace", ".world_initialized")
	if _, err := os.Stat(marker); err != nil {
		if err := os.MkdirAll(world, 0o755); err != nil {
			return err
		}
		// Try CLI, then module
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
	// Delegate to the existing local-intel setup logic via subprocess so we don't
	// duplicate the download + integrity check code. The binary must be in PATH.
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
// YAML snippet builder
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

	cfgPath := "~/.opsintelligence/opsintelligence.yaml"
	fmt.Println(Muted.Render("\n  Config location: " + cfgPath))
	fmt.Println(Muted.Render("  Re-run  opsintelligence doctor  to verify."))
	fmt.Println()
}

// ─────────────────────────────────────────────
// UI helpers
// ─────────────────────────────────────────────

func renderSetupHeader(ver string) string {
	robotStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	glowStyle := lipgloss.NewStyle().Foreground(ColorNeon).Bold(true)

	var robotSB strings.Builder
	for i, line := range robotLines {
		if i == 2 || i == 3 {
			robotSB.WriteString(glowStyle.Render(line) + "\n")
		} else {
			robotSB.WriteString(robotStyle.Render(line) + "\n")
		}
	}

	infoLines := []string{
		"",
		"  " + GradientWord("QUICKSTART") + "  " + Muted.Render(ver),
		Primary.Render("  Smart Mode Setup Wizard"),
		"",
		Muted.Render("  Sets up Gemma (on-device AI) +"),
		Muted.Render("  MemPalace (hierarchical memory)"),
		"",
		Muted.Render("  Takes ~2 min · needs internet for downloads"),
		"",
	}
	var infoSB strings.Builder
	for _, l := range infoLines {
		infoSB.WriteString(l + "\n")
	}

	combined := lipgloss.JoinHorizontal(lipgloss.Top, robotSB.String(), infoSB.String())
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(0, 1).
		Render(combined)

	return "\n" + box + "\n\n"
}

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
// Standalone progress-spinner bubbletea model
// (used internally if we need a full alt-screen spinner)
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
			return ErrorStyle.Render("✗ ") + Muted.Render(m.err.Error()) + "\n"
		}
		return lipgloss.NewStyle().Foreground(ColorCyan).Render("✓ ") + m.label + "\n"
	}
	return "  " + m.spinner.View() + " " + Muted.Render(m.label+"…") + "\n"
}
