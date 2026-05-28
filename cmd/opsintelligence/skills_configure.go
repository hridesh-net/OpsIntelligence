package main

// skills_configure.go — `opsintelligence skills configure` interactive TUI.
// Runs through the Rust form engine (Phase 5d); no Charmbracelet dependency.
//
// Usage:
//   opsintelligence skills configure
//   opsintelligence skills configure --tag productivity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/skills"
	"github.com/opsintelligence/opsintelligence/internal/tuibridge"
)

func skillsConfigureCmd(gf *globalFlags) *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Interactive TUI to select, install, and manage skills",
		Long: `Open an interactive multi-select TUI to choose which skills your agent should use.
You can also add custom skills from a local path or URL.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel, "")
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			return RunSkillsConfigure(cmd.Context(), cfg, gf.configPath, tag)
		},
	}
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Pre-filter skills by tag (e.g. productivity, ai, macos)")
	return cmd
}

// RunSkillsConfigure runs the interactive skill selection wizard. cfgPath may
// be "" to skip config writes (used from onboarding).
func RunSkillsConfigure(ctx context.Context, cfg *config.Config, cfgPath, tagFilter string) error {
	home, _ := os.UserHomeDir()
	bundledDir := filepath.Join(home, ".opsintelligence", "skills", "bundled")
	customDir := filepath.Join(home, ".opsintelligence", "skills", "custom")
	_ = os.MkdirAll(bundledDir, 0o755)
	_ = os.MkdirAll(customDir, 0o755)

	mp := skills.NewMarketplace(bundledDir, customDir)

	fmt.Println("  Fetching skill catalog…")
	index, err := mp.FetchIndex(ctx)
	if err != nil {
		return fmt.Errorf("could not load skill catalog: %w", err)
	}

	installedReg := skills.NewRegistry()
	_ = installedReg.LoadAll(ctx, customDir)
	enabledSet := make(map[string]bool)
	for _, s := range cfg.Agent.EnabledSkills {
		enabledSet[s] = true
	}

	entries := index.Skills
	if tagFilter != "" {
		var filtered []skills.MarketplaceEntry
		for _, e := range entries {
			for _, t := range e.Tags {
				if strings.EqualFold(t, tagFilter) {
					filtered = append(filtered, e)
					break
				}
			}
		}
		entries = filtered
		if len(entries) == 0 {
			return fmt.Errorf("no skills found for tag %q", tagFilter)
		}
	}

	const customSkillSentinel = "__custom__"

	var options []tuibridge.WizardOptionSpec
	var defaultSelected []string

	for _, e := range entries {
		label := e.Name
		if e.Emoji != "" {
			label = e.Emoji + "  " + e.Name
		}
		_, isInstalled := installedReg.Get(e.Name)
		switch {
		case isInstalled && enabledSet[e.Name]:
			label += "  ✔"
		case isInstalled:
			label += "  (installed, disabled)"
		}
		if e.Description != "" {
			label += " — " + truncateStr(e.Description, 72)
		}
		options = append(options, tuibridge.WizardOptionSpec{Value: e.Name, Label: label})
		if enabledSet[e.Name] || (len(cfg.Agent.EnabledSkills) == 0 && isInstalled) {
			defaultSelected = append(defaultSelected, e.Name)
		}
	}
	options = append(options, tuibridge.WizardOptionSpec{
		Value: customSkillSentinel,
		Label: "＋  Add custom skill  (local path or URL)",
	})

	var (
		selectedNames []string
		customPath    string
	)

	steps := []tuibridge.WizardStep{
		{
			Icon:     "🛠",
			Title:    "Configure agent skills",
			Subtitle: "Marketplace tools for the agent",
			Form: func() tuibridge.WizardFormSpec {
				return tuibridge.WizardFormSpec{
					Fields: []tuibridge.WizardFieldSpec{
						tuibridge.WizardMultiSelect("skills", "Skills to enable",
							"Space toggles · Enter confirms · ↑↓ moves. Bundled skills download automatically when missing.",
							defaultSelected, options),
					},
				}
			},
			OnSubmit: func(f map[string]any) error {
				selectedNames = tuibridge.WizardStrings(f, "skills", nil)
				return nil
			},
		},
		{
			Icon:  "🛠",
			Title: "Add custom skill",
			Skip: func() bool {
				for _, n := range selectedNames {
					if n == customSkillSentinel {
						return false
					}
				}
				return true
			},
			Form: func() tuibridge.WizardFormSpec {
				return tuibridge.WizardFormSpec{
					Fields: []tuibridge.WizardFieldSpec{
						tuibridge.WizardInput("path", "Custom skill path or URL",
							"Local dir: /path/to/my-skill   GitHub URL: https://github.com/user/skill",
							""),
					},
				}
			},
			OnSubmit: func(f map[string]any) error {
				customPath = strings.TrimSpace(tuibridge.WizardString(f, "path", ""))
				return nil
			},
		},
	}

	if err := tuibridge.RunWizard(ctx, tuibridge.WizardOptions{
		Brand: "OPSINTELLIGENCE",
		Steps: steps,
	}); err != nil {
		return err
	}

	// Process selection.
	var finalNames []string
	addCustom := false
	for _, n := range selectedNames {
		if n == customSkillSentinel {
			addCustom = true
			continue
		}
		finalNames = append(finalNames, n)
	}
	if addCustom && customPath != "" {
		fmt.Printf("  Installing custom skill from %s…\n", customPath)
		dest, err := installCustomSkill(ctx, mp, customPath)
		if err != nil {
			fmt.Printf("  ✗ Custom skill failed: %v\n", err)
		} else {
			name := filepath.Base(dest)
			finalNames = append(finalNames, name)
			fmt.Printf("  ✔ Custom skill %q added\n", name)
		}
	}

	if len(finalNames) == 0 {
		fmt.Println("  No skills selected — skipping.")
		return nil
	}

	fmt.Println()
	var installedOK []string
	for _, name := range finalNames {
		if _, ok := installedReg.Get(name); ok {
			installedOK = append(installedOK, name)
			continue
		}
		fmt.Printf("  Installing %s…", name)
		if _, err := mp.Install(ctx, name); err != nil {
			fmt.Printf(" ✗ failed: %v\n", err)
		} else {
			fmt.Println(" ✔")
			installedOK = append(installedOK, name)
		}
	}

	if cfgPath != "" && len(installedOK) > 0 {
		for _, name := range installedOK {
			if err := toggleSkillInConfig(cfgPath, name, true); err != nil {
				fmt.Printf("  ⚠ Could not update config for %s: %v\n", name, err)
			}
		}
	}

	fmt.Println()
	fmt.Printf("  ✔ %d skill(s) active\n", len(installedOK))
	fmt.Println("  Restart the agent to apply changes: opsintelligence restart")
	fmt.Println()
	return nil
}

// installCustomSkill handles both local path and URL installs.
func installCustomSkill(ctx context.Context, mp *skills.Marketplace, pathOrURL string) (string, error) {
	if !strings.HasPrefix(pathOrURL, "http") {
		return mp.InstallFromPath(expandHome(pathOrURL))
	}
	return mp.Install(ctx, pathOrURL)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
