package main

// prompts_cmd.go — `opsintelligence prompts` subcommand suite.
//
// Exposes the SmartPrompt library + chain runtime from the command line:
//
//   opsintelligence prompts ls                       # list chains + prompts
//   opsintelligence prompts show pr-review           # print a chain or prompt's full spec
//   opsintelligence prompts run pr-review --input pr_url=https://...
//
// This is the operator-facing counterpart of the in-process `chain_run`
// agent tool. Useful for smoke-testing new prompts without launching the
// full agent loop and for CI checks on the shipped library.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/prompts"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

func promptsCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prompts",
		Short: "Inspect and run the smart-prompt library (chains + meta prompts)",
		Long: `Inspect and run the smart-prompt library.

The library ships with DevOps-focused chains (pr-review, sonar-triage,
cicd-regression, incident-scribe) and meta prompts (self-critique,
evidence-extractor, plan-then-act). Operators can override any prompt
by dropping a *.md file into ~/.opsintelligence/prompts/.`,
	}
	cmd.AddCommand(
		promptsListCmd(gf),
		promptsShowCmd(gf),
		promptsRunCmd(gf),
	)
	return cmd
}

// ── prompts ls ───────────────────────────────────────────────────────

func promptsListCmd(gf *globalFlags) *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List available chains and smart prompts",
		RunE: func(_ *cobra.Command, _ []string) error {
			lib, err := openLibrary(gf)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KIND\tID\tNAME\tPURPOSE")
			for _, c := range lib.ListChains() {
				if tag != "" && !containsString(c.Tags, tag) {
					continue
				}
				fmt.Fprintf(tw, "chain\t%s\t%s\t%s\n", c.ID, c.Name, c.Purpose)
			}
			for _, p := range lib.ListPrompts() {
				if tag != "" && !containsString(p.Tags, tag) {
					continue
				}
				fmt.Fprintf(tw, "prompt\t%s\t%s\t%s\n", p.ID, p.Name, p.Purpose)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "Filter by tag (e.g. 'meta', 'devops', 'github')")
	return cmd
}

// ── prompts show ─────────────────────────────────────────────────────

func promptsShowCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Print the full spec (frontmatter + body) of a chain or prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			lib, err := openLibrary(gf)
			if err != nil {
				return err
			}
			id := args[0]
			if c, ok := lib.Chain(id); ok {
				b, _ := yaml.Marshal(c)
				fmt.Println("# chain")
				fmt.Println(string(b))
				fmt.Println("\n# resolved steps")
				for _, s := range c.Steps {
					p, ok := lib.Prompt(s)
					if !ok {
						fmt.Printf("  - %s  (MISSING!)\n", s)
						continue
					}
					fmt.Printf("  - %s  [%s]\n", s, p.SourcePath)
				}
				return nil
			}
			if p, ok := lib.Prompt(id); ok {
				b, _ := yaml.Marshal(p)
				fmt.Println("# prompt")
				fmt.Println(string(b))
				fmt.Println("# body")
				fmt.Println(p.Body)
				return nil
			}
			return fmt.Errorf("no chain or prompt named %q — try `opsintelligence prompts ls`", id)
		},
	}
}

// ── prompts run ──────────────────────────────────────────────────────

func promptsRunCmd(gf *globalFlags) *cobra.Command {
	var inputs []string
	var inputsFile string
	var kind string
	var noTrace bool
	var outPath string
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Execute a chain (or single prompt) and print the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			lib, err := openLibraryFromConfig(cfg)
			if err != nil {
				return err
			}

			parsed := map[string]any{}
			if inputsFile != "" {
				data, readErr := os.ReadFile(inputsFile)
				if readErr != nil {
					return fmt.Errorf("read inputs file: %w", readErr)
				}
				if err := json.Unmarshal(data, &parsed); err != nil {
					return fmt.Errorf("parse inputs json: %w", err)
				}
			}
			for _, kv := range inputs {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("--input must be key=value, got %q", kv)
				}
				parsed[strings.TrimSpace(k)] = v
			}

			ctx := context.Background()
			reg := provider.NewRegistry()
			if err := registerProviders(ctx, cfg, reg, log); err != nil {
				return fmt.Errorf("register providers: %w", err)
			}
			resolved := cfg.Routing.Default
			if resolved == "" {
				for _, pr := range reg.All() {
					models, _ := pr.ListModels(ctx)
					if len(models) > 0 {
						resolved = pr.Name() + "/" + models[0].ID
						break
					}
				}
			}
			if resolved == "" {
				return fmt.Errorf("no model configured — set routing.default in config")
			}
			p, modelInfo, err := reg.ResolveModel(ctx, resolved)
			if err != nil {
				return fmt.Errorf("resolve model %q: %w", resolved, err)
			}
			runner := &prompts.Runner{
				Provider:     p,
				Lib:          lib,
				DefaultModel: modelInfo.ID,
			}

			var (
				out   io.Writer = os.Stdout
				final string
			)
			if outPath != "" {
				f, err := os.Create(outPath)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}

			useChain := true
			switch kind {
			case "chain":
			case "prompt":
				useChain = false
			case "", "auto":
				if _, ok := lib.Chain(id); !ok {
					useChain = false
				}
			default:
				return fmt.Errorf("unknown --kind %q (expected chain|prompt|auto)", kind)
			}

			if useChain {
				res, err := runner.RunChain(ctx, id, parsed)
				if err != nil {
					return err
				}
				if !noTrace {
					fmt.Fprintf(out, "# chain: %s  (%d steps, %s)\n\n", res.ChainID, len(res.Steps), res.Latency.Truncate(1e6))
					for i, s := range res.Steps {
						fmt.Fprintf(out, "## step %d — %s  (%s, %d tok)\n", i+1, s.PromptID, s.Latency.Truncate(1e6), s.Usage.TotalTokens)
						fmt.Fprintln(out, s.Output)
						fmt.Fprintln(out)
					}
					fmt.Fprintln(out, "## final")
				}
				fmt.Fprintln(out, res.Final)
				final = res.Final
			} else {
				step, err := runner.RunPrompt(ctx, id, parsed)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, step.Output)
				final = step.Output
			}
			_ = final
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&inputs, "input", nil, "Input key=value (repeatable, e.g. --input pr_url=https://...)")
	cmd.Flags().StringVar(&inputsFile, "inputs-file", "", "Path to a JSON object to supply as chain/prompt inputs")
	cmd.Flags().StringVar(&kind, "kind", "auto", "How to resolve the id: chain|prompt|auto")
	cmd.Flags().BoolVar(&noTrace, "no-trace", false, "Only print the final output (no per-step trace)")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "Write the result to this file instead of stdout")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────

func openLibrary(gf *globalFlags) (*prompts.Library, error) {
	log := buildLogger(gf.logLevel)
	cfg, err := loadConfig(gf.configPath, log)
	if err != nil {
		return nil, err
	}
	return openLibraryFromConfig(cfg)
}

func openLibraryFromConfig(cfg *config.Config) (*prompts.Library, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	embed, err := config.EmbeddedPromptsFS()
	if err != nil {
		return nil, fmt.Errorf("embedded prompts fs: %w", err)
	}
	return prompts.Loader{
		Embedded:     embed,
		EmbeddedRoot: ".",
		ExtraDirs:    cfg.SmartPrompts.ExtraSourceDirs,
		Dir:          filepath.Join(cfg.StateDir, "prompts"),
	}.Load()
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
