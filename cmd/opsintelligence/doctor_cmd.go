package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/internal/channels/slack"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/localintel"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// doctorCheck is one row of opsintelligence doctor output (text or JSON).
// Details is optional structured metadata (file/line for config issues, etc.).
type doctorCheck struct {
	ID       string            `json:"id"`
	Severity string            `json:"severity"` // ok | warn | error | skipped
	Message  string            `json:"message"`
	Details  map[string]string `json:"details,omitempty"`
}

// doctorOutput is the machine-readable shape for opsintelligence doctor --json.
// Backward compatibility: new top-level fields may be added; breaking changes bump schema_version.
type doctorOutput struct {
	SchemaVersion int           `json:"schema_version"`
	ConfigPath    string        `json:"config_path,omitempty"` // resolved path when a config file was read
	ExitCode      int           `json:"exit_code"`             // same as process exit: 0 ok, 1 warnings only, 2 errors
	Checks        []doctorCheck `json:"checks"`
}

func doctorCmd(flags *globalFlags) *cobra.Command {
	var asJSON, skipNetwork, noInput bool
	var channelTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate config, LLM providers, and channel connectivity (read-only API checks)",
		Long: `Runs non-destructive checks:

  • Loads and validates opsintelligence.yaml (schema, required keys for enabled features, deprecated keys).
  • For each configured LLM provider: HealthCheck (list models / minimal API) with bounded timeouts.
  • For Slack: token format check, then auth.test API call.
  • For incoming webhooks: documents that the gateway must be running for public ingress (not probed by default).

Exit codes: 0 = all OK, 1 = warnings only (no errors), 2 = config or check errors.

Use --json for machine-readable output (stdout only; config load errors go to stderr). Use --skip-network to skip outbound API calls (LLM providers + chat APIs; e.g. air-gapped CI).

Use --no-input / --non-interactive for scripts (doctor does not prompt today; reserved for future checks).

See doc/runbooks/doctor-config-validation.md and doc/runbooks/doctor-json-schema.md for details.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = noInput // reserved: fail closed if a future check ever required stdin
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			cfg, usedPath, cfgIssues, err := loadConfigForDoctor(flags.configPath)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "config: %v\n", err)
				os.Exit(2)
			}

			checks := doctorChecksFromConfigIssues(usedPath, cfgIssues)
			checks = append(checks, runDoctorChecks(ctx, cfg, skipNetwork, flags.logLevel, channelTimeout)...)

			exit := doctorExitCode(checks)

			if asJSON {
				out := doctorOutput{
					SchemaVersion: 1,
					ConfigPath:    usedPath,
					ExitCode:      exit,
					Checks:        checks,
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
			} else {
				fmt.Fprint(cmd.OutOrStdout(), formatDoctorTextOutput(checks))
			}

			if exit != 0 {
				os.Exit(exit)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON to stdout (schema_version, exit_code, checks; see doc/runbooks/doctor-json-schema.md)")
	cmd.Flags().BoolVar(&skipNetwork, "skip-network", false, "Skip outbound API checks (LLM providers + Slack)")
	cmd.Flags().DurationVar(&channelTimeout, "channel-timeout", 15*time.Second, "Per-channel timeout for Slack API check")
	cmd.Flags().BoolVar(&noInput, "no-input", false, "Non-interactive: do not require stdin (reserved; doctor does not prompt)")
	cmd.Flags().BoolVar(&noInput, "non-interactive", false, "Alias for --no-input")
	return cmd
}

// formatDoctorTextOutput renders doctor checks as CLI text. Checks are sorted by id for stable snapshots and support.
func formatDoctorTextOutput(checks []doctorCheck) string {
	sorted := append([]doctorCheck(nil), checks...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ID != sorted[j].ID {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].Severity < sorted[j].Severity
	})
	var b strings.Builder
	for _, c := range sorted {
		fmt.Fprintf(&b, "[%s] %s\n  %s\n", c.Severity, c.ID, c.Message)
	}
	return b.String()
}

// loadConfigForDoctor loads YAML when the file exists (with structured validation issues); otherwise env-only defaults.
// usedPath is the config file path when a file was read; empty when using env-only defaults.
func loadConfigForDoctor(configPath string) (*config.Config, string, []config.DoctorIssue, error) {
	path := configPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	_, statErr := os.Stat(path)
	if statErr == nil {
		cfg, issues, err := config.LoadForDoctor(path)
		return cfg, path, issues, err
	}
	if !os.IsNotExist(statErr) {
		return nil, "", nil, statErr
	}
	return config.LoadFromEnv(), "", nil, nil
}

func doctorChecksFromConfigIssues(usedPath string, issues []config.DoctorIssue) []doctorCheck {
	if usedPath != "" && len(issues) == 0 {
		return []doctorCheck{{
			ID:       "config.validate",
			Severity: "ok",
			Message:  fmt.Sprintf("%s: parsed and validated", usedPath),
			Details:  map[string]string{"config_path": usedPath},
		}}
	}
	out := make([]doctorCheck, 0, len(issues))
	for _, i := range issues {
		id := "config.validate"
		switch {
		case strings.Contains(i.Message, "deprecated key"):
			id = "config.deprecated"
		case strings.Contains(i.Message, "top-level `version`"):
			id = "config.version"
		}
		sev := i.Severity
		if sev == "" {
			sev = "warn"
		}
		msg := i.Message
		if i.Line > 0 {
			msg = fmt.Sprintf("%s:%d:%d: %s", i.File, i.Line, i.Column, i.Message)
		} else if i.File != "" {
			msg = fmt.Sprintf("%s: %s", i.File, msg)
		}
		ch := doctorCheck{ID: id, Severity: sev, Message: msg}
		if i.Line > 0 && i.File != "" {
			ch.Details = map[string]string{
				"file":   i.File,
				"line":   fmt.Sprintf("%d", i.Line),
				"column": fmt.Sprintf("%d", i.Column),
			}
		}
		out = append(out, ch)
	}
	return out
}

func doctorExitCode(checks []doctorCheck) int {
	hasErr, hasWarn := false, false
	for _, c := range checks {
		switch c.Severity {
		case "error":
			hasErr = true
		case "warn":
			hasWarn = true
		}
	}
	if hasErr {
		return 2
	}
	if hasWarn {
		return 1
	}
	return 0
}

func runDoctorChecks(ctx context.Context, cfg *config.Config, skipNetwork bool, logLevel string, channelTimeout time.Duration) []doctorCheck {
	var checks []doctorCheck

	checks = append(checks, runDoctorProviderChecks(ctx, cfg, skipNetwork, logLevel)...)

	if skipNetwork {
		checks = append(checks, doctorCheck{
			ID:       "channels.network",
			Severity: "skipped",
			Message:  "Channel API checks skipped (--skip-network).",
		})
	} else {
		checks = append(checks, pingSlack(ctx, cfg, channelTimeout)...)
	}

	checks = append(checks, runDoctorDevOpsChecks(ctx, cfg, skipNetwork, channelTimeout)...)

	checks = append(checks, checkWebhooks(cfg))
	checks = append(checks, checkLocalIntel(cfg))
	return checks
}

const doctorProviderCheckTimeout = 45 * time.Second

// runDoctorProviderChecks uses the same registration path as the agent and [Registry.CheckAll]
// (HealthCheck per provider). Errors are sanitized so API keys are not echoed.
func runDoctorProviderChecks(ctx context.Context, cfg *config.Config, skipNetwork bool, logLevel string) []doctorCheck {
	if skipNetwork {
		return []doctorCheck{{
			ID:       "provider.health",
			Severity: "skipped",
			Message:  "LLM provider API checks skipped (--skip-network).",
		}}
	}

	log := buildLogger(logLevel)
	reg := provider.NewRegistry()
	_ = registerProviders(ctx, cfg, reg, log)

	all := reg.All()
	if len(all) == 0 {
		return []doctorCheck{{
			ID:       "provider.health",
			Severity: "skipped",
			Message:  "No LLM providers registered; add providers.* to opsintelligence.yaml (or set provider env vars).",
		}}
	}

	pctx, cancel := context.WithTimeout(ctx, doctorProviderCheckTimeout)
	defer cancel()

	report := reg.CheckAll(pctx)
	names := make([]string, 0, len(report.Results))
	for n := range report.Results {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]doctorCheck, 0, len(names))
	for _, name := range names {
		r := report.Results[name]
		id := "provider." + name
		if r.OK {
			out = append(out, doctorCheck{
				ID:       id,
				Severity: "ok",
				Message:  fmt.Sprintf("%s: reachable (HealthCheck / list models).", name),
			})
			continue
		}
		out = append(out, doctorCheck{
			ID:       id,
			Severity: "error",
			Message:  formatProviderHealthMessage(name, r.Error),
		})
	}
	return out
}

var (
	reDoctorOpenAIKey = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]{8,}\b`)
	reDoctorBearer    = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`)
)

// sanitizeDoctorMessage removes common secret patterns from strings that might appear in HTTP errors.
func sanitizeDoctorMessage(s string) string {
	s = reDoctorOpenAIKey.ReplaceAllString(s, "sk-…")
	s = reDoctorBearer.ReplaceAllString(s, "Bearer …")
	return s
}

func formatProviderHealthMessage(providerName, errStr string) string {
	errStr = strings.TrimSpace(errStr)
	msg := errStr
	if msg == "" {
		msg = providerName + ": unknown error"
	} else if !strings.HasPrefix(msg, providerName+":") {
		msg = providerName + ": " + msg
	}
	msg = sanitizeDoctorMessage(msg)
	lower := strings.ToLower(msg)
	hint := ""
	switch {
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized"):
		hint = " (authentication failed — check API key / token)"
	case strings.Contains(lower, "403") || strings.Contains(lower, "forbidden"):
		hint = " (forbidden — key scope, billing, or org policy)"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "context deadline"):
		hint = " (timeout — check network, proxy, or firewall)"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "dns"):
		hint = " (network/DNS — check base URL and connectivity)"
	}
	return msg + hint
}

func checkLocalIntel(cfg *config.Config) doctorCheck {
	if !cfg.Agent.LocalIntel.Enabled {
		return doctorCheck{
			ID:       "agent.local_intel",
			Severity: "skipped",
			Message:  "agent.local_intel.enabled is false.",
		}
	}
	var warnings []string
	if !localintel.CompiledWithLocalGemma() {
		warnings = append(warnings, "this binary does not include in-process Gemma support (use official release v3.10.15+ or build with tag opsintelligence_localgemma)")
	}
	p := strings.TrimSpace(cfg.Agent.LocalIntel.GGUFPath)
	if p == "" {
		p = strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF"))
	}
	if p != "" {
		if _, err := os.Stat(p); err != nil {
			warnings = append(warnings, fmt.Sprintf("GGUF not readable at %q: %v", p, err))
		}
	} else if len(localintel.Gemma4E2BGGUF) == 0 {
		warnings = append(warnings, "run `opsintelligence local-intel setup` (or set agent.local_intel.gguf_path / OPSINTELLIGENCE_LOCAL_GEMMA_GGUF), or build with opsintelligence_embedlocalgemma and ship embedded weights")
	}
	if len(warnings) > 0 {
		return doctorCheck{
			ID:       "agent.local_intel",
			Severity: "warn",
			Message:  strings.Join(warnings, " | "),
		}
	}
	return doctorCheck{
		ID:       "agent.local_intel",
		Severity: "ok",
		Message:  "local_intel is enabled; GGUF/embed path looks consistent with this binary.",
	}
}

func pingSlack(ctx context.Context, cfg *config.Config, channelTimeout time.Duration) []doctorCheck {
	ch := cfg.Channels.Slack
	if ch == nil || ch.BotToken == "" || ch.AppToken == "" {
		return []doctorCheck{{
			ID:       "channel.slack",
			Severity: "skipped",
			Message:  "Not configured (need channels.slack bot_token + app_token).",
		}}
	}
	if err := validateSlackTokenFormats(ch.BotToken, ch.AppToken); err != nil {
		return []doctorCheck{{
			ID:       "channel.slack",
			Severity: "error",
			Message:  formatChannelTokenError("slack token format", err),
		}}
	}
	sl, err := slack.New(ch.BotToken, ch.AppToken, ch.DMMode, ch.AllowFrom)
	if err != nil {
		return []doctorCheck{{
			ID:       "channel.slack",
			Severity: "error",
			Message:  formatChannelPingError("channel.slack", "init", err),
		}}
	}
	pctx, cancel := context.WithTimeout(ctx, channelTimeout)
	defer cancel()
	if err := sl.Ping(pctx); err != nil {
		return []doctorCheck{{
			ID:       "channel.slack",
			Severity: "error",
			Message:  formatChannelPingError("channel.slack", "auth.test", err),
		}}
	}
	return []doctorCheck{{
		ID:       "channel.slack",
		Severity: "ok",
		Message:  "Slack: token prefixes OK; auth.test succeeded (workspace + socket credentials).",
	}}
}

