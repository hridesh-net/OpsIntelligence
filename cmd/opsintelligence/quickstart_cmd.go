package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/localintel"
	"github.com/opsintelligence/opsintelligence/internal/tuibridge"
	"github.com/spf13/cobra"
)

// quickstartCmd registers `opsintelligence quickstart` — a guided wizard for
// installing the optional smart-mode components (MemPalace memory + on-device
// Gemma). The wizard runs through the Rust TUI form engine; no Charmbracelet
// dependency.
func quickstartCmd(gf *globalFlags) *cobra.Command {
	var stateDir, ggufPath, bootstrapPy string

	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Interactive wizard: set up Gemma (on-device AI) and MemPalace (memory)",
		Long: `quickstart walks you through installing two optional smart-mode components:

  • MemPalace  — hierarchical memory system backed by Python mempalace PyPI package
  • Gemma      — on-device GGUF model for fast local routing (~3 GiB download)

Both components are optional — skip either one by answering "No" in the wizard.

Prerequisites:
  • MemPalace: Python 3.9+ with the venv module (python3 in PATH)
  • Gemma:     ~3 GiB free disk space; internet connection for download`,

		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			sd := strings.TrimSpace(stateDir)
			if sd == "" {
				if cfg, err := mempalaceLoadCfg(gf, ""); err == nil {
					sd = cfg.StateDir
				}
			}
			if sd == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("quickstart: cannot resolve home dir: %w", err)
				}
				sd = filepath.Join(home, ".opsintelligence")
			}

			opts := SetupOptions{
				StateDir:        sd,
				GGUFPath:        strings.TrimSpace(ggufPath),
				BootstrapPython: strings.TrimSpace(bootstrapPy),
			}
			result := &SetupResult{}

			pyOK := pythonAvailable(opts.BootstrapPython)

			steps := []tuibridge.WizardStep{
				{
					Icon:     "✨",
					Title:    "Quickstart",
					Subtitle: "Choose which optional components to install",
					Form: func() tuibridge.WizardFormSpec {
						memDesc := "Hierarchical memory backed by the mempalace Python package."
						if !pyOK {
							memDesc = "⚠ python3 not found in PATH — MemPalace will be skipped."
						}
						return tuibridge.WizardFormSpec{
							Fields: []tuibridge.WizardFieldSpec{
								tuibridge.WizardConfirm("mempalace", "Install MemPalace?",
									memDesc, pyOK, "Yes", "No"),
								tuibridge.WizardConfirm("gemma", "Install Local Gemma model?",
									"On-device GGUF model for routing (~3 GiB download).",
									false, "Yes", "No"),
							},
						}
					},
					OnSubmit: func(f map[string]any) error {
						result.MemPalaceEnabled = pyOK && tuibridge.WizardBool(f, "mempalace", false)
						result.GemmaEnabled = tuibridge.WizardBool(f, "gemma", false)
						return nil
					},
				},
				{
					Icon:            "🧩",
					Title:           "MemPalace",
					SideEffectLabel: "Creating venv and installing mempalace",
					Skip:            func() bool { return !result.MemPalaceEnabled },
					SideEffect: func() error {
						return runMemPalaceSetup(ctx, opts)
					},
				},
				{
					Icon:            "🪶",
					Title:           "Local Gemma",
					SideEffectLabel: "Downloading + verifying Gemma GGUF",
					Skip:            func() bool { return !result.GemmaEnabled },
					SideEffect: func() error {
						dest := opts.GGUFPath
						if dest == "" {
							dest = localintel.DefaultGGUFPath(opts.StateDir)
						}
						if err := runGemmaSetup(ctx, dest); err != nil {
							return err
						}
						result.GGUFPath = dest
						return nil
					},
				},
			}

			if err := tuibridge.RunWizard(ctx, tuibridge.WizardOptions{
				Brand:  "OPSINTELLIGENCE",
				Steps:  steps,
				OnDone: doneSummaryQuickstart(result),
			}); err != nil {
				return fmt.Errorf("quickstart: %w", err)
			}

			if !result.MemPalaceEnabled && !result.GemmaEnabled {
				fmt.Println("Nothing was set up. Run `opsintelligence quickstart` again when ready.")
				return nil
			}
			printQuickstartYAML(result, opts)
			return nil
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "",
		"OpsIntelligence state directory (default: ~/.opsintelligence)")
	cmd.Flags().StringVar(&ggufPath, "gguf-path", "",
		"Custom GGUF destination path (default: <state-dir>/models/gemma-4-e2b-it.gguf)")
	cmd.Flags().StringVar(&bootstrapPy, "python", "",
		"Python interpreter to use for MemPalace venv (default: python3)")

	return cmd
}

func doneSummaryQuickstart(r *SetupResult) string {
	parts := []string{}
	if r.MemPalaceEnabled {
		parts = append(parts, "MemPalace installed")
	}
	if r.GemmaEnabled {
		parts = append(parts, "Gemma installed")
	}
	if len(parts) == 0 {
		return "Skipped — nothing was installed."
	}
	return strings.Join(parts, " · ") + " — paste the YAML snippet printed after exit into opsintelligence.yaml."
}

func printQuickstartYAML(r *SetupResult, opts SetupOptions) {
	if !r.MemPalaceEnabled && !r.GemmaEnabled {
		return
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
	fmt.Println()
	fmt.Println("▸ Add to opsintelligence.yaml (merge with existing memory:/agent: blocks):")
	fmt.Println()
	fmt.Println(sb.String())
	fmt.Println("Config location: ~/.opsintelligence/opsintelligence.yaml")
	fmt.Println("Re-run  opsintelligence doctor  to verify.")
	fmt.Println()
}
