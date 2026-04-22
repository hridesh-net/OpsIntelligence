package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	"github.com/spf13/cobra"
)

// quickstartCmd registers the `opsintelligence quickstart` command — a guided
// TUI wizard that sets up Gemma (on-device routing) and MemPalace (memory)
// without requiring the user to edit YAML directly.
func quickstartCmd(gf *globalFlags) *cobra.Command {
	var stateDir, ggufPath, bootstrapPy string

	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Interactive wizard: set up Gemma (on-device AI) and MemPalace (memory)",
		Long: `quickstart walks you through installing two optional smart-mode components:

  • MemPalace  — hierarchical memory system backed by Python mempalace PyPI package
  • Gemma      — on-device GGUF model for fast local routing (~3 GiB download)

It detects what's already installed, runs the setup, and prints the YAML snippet
you need to add to your opsintelligence.yaml.

Both components are optional — skip either one by answering "No" in the wizard.

Prerequisites:
  • MemPalace: Python 3.9+ with the venv module (python3 in PATH)
  • Gemma:     ~3 GiB free disk space; internet connection for download`,

		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Resolve state directory
			sd := strings.TrimSpace(stateDir)
			if sd == "" {
				// Try loading from config, fall back to default
				cfg, err := mempalaceLoadCfg(gf, "")
				if err == nil {
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

			opts := tui.SetupOptions{
				StateDir:        sd,
				GGUFPath:        strings.TrimSpace(ggufPath),
				BootstrapPython: strings.TrimSpace(bootstrapPy),
				Version:         version,
			}

			result, err := tui.RunSetupWizard(ctx, opts)
			if err != nil {
				return fmt.Errorf("quickstart: %w", err)
			}

			if !result.MemPalaceEnabled && !result.GemmaEnabled {
				fmt.Println(tui.Muted.Render("Nothing was set up. Run `opsintelligence quickstart` again when ready."))
				return nil
			}

			fmt.Println(tui.Primary.Render("▸ Done! Next steps:"))
			if result.MemPalaceEnabled || result.GemmaEnabled {
				fmt.Println(tui.Muted.Render("  1. Paste the YAML snippet above into your opsintelligence.yaml"))
				fmt.Println(tui.Muted.Render("  2. Run: opsintelligence doctor   (verify everything is reachable)"))
				fmt.Println(tui.Muted.Render("  3. Run: opsintelligence agent    (start chatting)"))
			}
			fmt.Println()
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
