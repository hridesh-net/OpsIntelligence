// Package main is the OpsIntelligence CLI entry point.
// It builds the full dependency graph and dispatches to subcommands.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	chadapter "github.com/opsintelligence/opsintelligence/internal/channels/adapter"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/automation"
	"github.com/opsintelligence/opsintelligence/internal/autotool"
	"github.com/opsintelligence/opsintelligence/internal/channels/slack"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/cron"
	"github.com/opsintelligence/opsintelligence/internal/embeddings"
	embedproviders "github.com/opsintelligence/opsintelligence/internal/embeddings/providers"
	"github.com/opsintelligence/opsintelligence/internal/extensions"
	"github.com/opsintelligence/opsintelligence/internal/gateway"
	"github.com/opsintelligence/opsintelligence/internal/graph"
	"github.com/opsintelligence/opsintelligence/internal/mcp"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/mempalace"
	obstracing "github.com/opsintelligence/opsintelligence/internal/observability/tracing"
	"github.com/opsintelligence/opsintelligence/internal/prompts"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/provider/anthropic"
	"github.com/opsintelligence/opsintelligence/internal/provider/bedrock"
	"github.com/opsintelligence/opsintelligence/internal/provider/catalogs"
	"github.com/opsintelligence/opsintelligence/internal/provider/ollama"
	"github.com/opsintelligence/opsintelligence/internal/provider/openai"
	"github.com/opsintelligence/opsintelligence/internal/provider/openaicompat"
	planoprovider "github.com/opsintelligence/opsintelligence/internal/provider/plano"
	"github.com/opsintelligence/opsintelligence/internal/provider/vertex"
	"github.com/opsintelligence/opsintelligence/internal/security"
	"github.com/opsintelligence/opsintelligence/internal/skills"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
	"github.com/opsintelligence/opsintelligence/internal/system"
	"github.com/opsintelligence/opsintelligence/internal/tools"
	"github.com/opsintelligence/opsintelligence/internal/voice"
	"github.com/opsintelligence/opsintelligence/internal/webhookadapter"
	ghadapter "github.com/opsintelligence/opsintelligence/internal/webhookadapter/github"
	_ "github.com/opsintelligence/opsintelligence/internal/webui" // ensure embed FS is included
)

var version = "v3.10.26" // Overridden by -ldflags "-X main.version=..." during build

type reliableToolSender struct {
	rs *chadapter.ReliableSender
}

func (s reliableToolSender) SendText(ctx context.Context, sessionID, text string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session_id is required for outbound channel sends")
	}
	_, err := s.rs.Send(ctx, chadapter.OutboundMessage{
		SessionID: sessionID,
		Text:      text,
	})
	return err
}

// defaultHeartbeatPrompt matches AGENTS.md guidance for periodic heartbeat polls.
const defaultHeartbeatPrompt = `Read HEARTBEAT.md if it exists in your workspace (state directory). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK.`

// runHeartbeatLoop issues periodic synthetic user turns on a dedicated session (logs only;
// use cron + channels if you need scheduled output delivered to Slack).
func runHeartbeatLoop(ctx context.Context, base *agent.Runner, interval time.Duration, sessionID, prompt string, log *zap.Logger) {
	if interval < time.Minute {
		interval = time.Minute
	}
	if sessionID == "" {
		sessionID = "opsintelligence:heartbeat"
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultHeartbeatPrompt
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var mu sync.Mutex
	log.Info("heartbeat scheduler started",
		zap.String("session_id", sessionID),
		zap.Duration("interval", interval),
	)
	for {
		select {
		case <-ctx.Done():
			log.Info("heartbeat scheduler stopped")
			return
		case <-t.C:
			mu.Lock()
			hbCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
			hr := base.WithSession(sessionID)
			_, err := hr.Run(hbCtx, memory.Message{
				ID:        uuid.New().String(),
				SessionID: sessionID,
				Role:      memory.RoleUser,
				Content:   prompt,
				CreatedAt: time.Now(),
			})
			cancel()
			if err != nil {
				log.Warn("heartbeat tick failed", zap.Error(err))
			} else {
				log.Debug("heartbeat tick completed")
			}
			mu.Unlock()
		}
	}
}

// agentPlanningEnabled defaults to true when unset (upfront planning on).
func agentPlanningEnabled(c *config.Config) bool {
	if c.Agent.Planning == nil {
		return true
	}
	return *c.Agent.Planning
}

// agentReflectionEnabled defaults to false when unset (extra LLM call; opt-in).
func agentReflectionEnabled(c *config.Config) bool {
	if c.Agent.Reflection == nil {
		return false
	}
	return *c.Agent.Reflection
}

func main() {
	if tui.ShouldPrintStartupBanner() {
		tui.MaybePrintCLIHeader(version)
	}
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────
// Global flags
// ─────────────────────────────────────────────

type globalFlags struct {
	configPath string
	logLevel   string
	noColor    bool
}

func rootCmd() *cobra.Command {
	flags := &globalFlags{}

	root := &cobra.Command{
		Use:   "opsintelligence",
		Short: "OpsIntelligence — autonomous DevOps agent",
		Long: `OpsIntelligence is a team-configurable autonomous DevOps agent with:
  • Built-in DevOps skill graph: PR review, SonarQube triage, CI/CD regression
    detection, incident scribing, runbooks (see skills/devops)
  • gh-pr-review skill: gh pr checkout into disposable worktrees, local lint/test
    runs, and one-click GitHub "suggestion" blocks via the Reviews API
  • Smart-prompt chains (pr-review, sonar-triage, cicd-regression,
    incident-scribe) with built-in self-critique, exposed via chain_run
  • Policy-driven team overrides under teams/<team>/ and
    <state_dir>/policies/ (owner-only, read-only by default)
  • 15+ LLM providers, embeddings, three-tier memory (Working / Episodic /
    Semantic), MCP clients, cron, webhooks, and Slack notifications`,
		Version:      version,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(&flags.configPath, "config", "c", "", "Config file path (default: ~/.opsintelligence/opsintelligence.yaml)")
	root.PersistentFlags().StringVar(&flags.logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	root.PersistentFlags().BoolVar(&flags.noColor, "no-color", false, "Disable color output")

	root.AddCommand(
		autoCmd(flags),
		agentCmd(flags),
		startCmd(flags),
		stopCmd(flags),
		statusCmd(flags),
		restartCmd(flags),
		providersCmd(flags),
		embeddingsCmd(flags),
		memoryCmd(flags),
		mempalaceCmd(flags),
		toolsCmd(flags),
		localIntelCmd(flags),
		extensionsCmd(flags),
		gatewayCmd(flags),
		onboardCmd(flags),
		skillsCmd(flags),
		promptsCmd(flags),
		mcpCmd(flags),
		serviceCmd(flags),
		securityCmd(flags),
		logicTestCmd(flags),
		cronCmd(flags),
		dlqCmd(flags),
		datastoreCmd(flags),
		adminCmd(flags),
		doctorCmd(flags),
		versionCmd(flags),
		localgemmaCmd(flags),
	)
	return root
}

// ─────────────────────────────────────────────
// agent command
// ─────────────────────────────────────────────

func autoCmd(gf *globalFlags) *cobra.Command {
	var (
		model     string
		sessionID string
	)

	cmd := &cobra.Command{
		Use:   "auto [goal]",
		Short: "Start a continuous autonomous agent loop targeting a specific goal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(gf, gf.configPath, model, args[0], sessionID, false, false, true)
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "Model to use (e.g. anthropic/claude-3-5-sonnet)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Resume an existing session by ID")
	return cmd
}

func agentCmd(gf *globalFlags) *cobra.Command {
	var (
		message   string
		model     string
		noStream  bool
		sessionID string
		serve     bool
	)

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Start an interactive agent session or send a single message",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(gf, gf.configPath, model, message, sessionID, serve, noStream, false)
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "Single message to send (non-interactive)")
	cmd.Flags().StringVar(&model, "model", "", "Model to use (e.g. anthropic/claude-haiku-3-5)")
	cmd.Flags().BoolVar(&noStream, "no-stream", false, "Disable streaming output")
	cmd.Flags().StringVar(&sessionID, "session", "", "Resume an existing session by ID")
	cmd.Flags().BoolVarP(&serve, "serve", "s", false, "Run in background mode with Gateway and messaging channels active")
	return cmd
}

// ─────────────────────────────────────────────
// start / stop / status / restart commands
// ─────────────────────────────────────────────

func startCmd(gf *globalFlags) *cobra.Command {
	var daemon bool
	var skipPreflight, preflightFull bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start OpsIntelligence in background mode",
		Long: `Starts OpsIntelligence with the gateway and messaging channels.

By default, runs a fast preflight (same checks as opsintelligence doctor --skip-network) before binding ports. Use --preflight-full to probe LLM and channel APIs. Use --skip-preflight only if you trust this environment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return execStart(gf, daemon, skipPreflight, preflightFull, cmd)
		},
	}
	cmd.Flags().BoolVarP(&daemon, "daemon", "d", false, "Run detached in the background")
	registerPreflightFlags(cmd, &skipPreflight, &preflightFull)
	return cmd
}

func stopCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background OpsIntelligence process",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			pidFile := PidFile(cfg.StateDir)
			pid, err := ReadPID(pidFile)
			if err != nil {
				return fmt.Errorf("agent not running (no PID file)")
			}
			if !CheckPID(pid) {
				_ = os.Remove(pidFile)
				return fmt.Errorf("agent not running (stale PID file)")
			}
			process, _ := os.FindProcess(pid)
			if err := process.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to stop agent: %w", err)
			}
			fmt.Printf("Stopping OpsIntelligence (PID: %d)...\n", pid)
			_ = os.Remove(pidFile)
			return nil
		},
	}
}

func statusCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check status, CPU, and RAM usage of OpsIntelligence",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			pid, err := ReadPID(PidFile(cfg.StateDir))
			if err != nil || !CheckPID(pid) {
				fmt.Println("● OpsIntelligence is NOT running.")
				fmt.Printf("  Start with: opsintelligence start\n")
				return nil
			}

			// Count installed skills
			skillCount := 0
			customDir := filepath.Join(cfg.StateDir, "skills", "custom")
			if entries, err := os.ReadDir(customDir); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						skillCount++
					}
				}
			}
			enabledCount := len(cfg.Agent.EnabledSkills)
			skillSummary := fmt.Sprintf("%d installed", skillCount)
			if enabledCount > 0 {
				skillSummary = fmt.Sprintf("%d enabled / %d installed", enabledCount, skillCount)
			}

			var channels []string
			if cfg.Channels.Slack != nil {
				channels = append(channels, "Slack")
			}

			// MCP transport
			mcpTransport := cfg.MCP.Server.Transport
			if mcpTransport == "" {
				mcpTransport = "stdio"
			}

			return tui.RunStatus(tui.StatusInfo{
				PID:           pid,
				Version:       version,
				SkillSummary:  skillSummary,
				Channels:      channels,
				PlanoEnabled:  cfg.Plano.Enabled,
				PlanoEndpoint: cfg.Plano.Endpoint,
				MCPEnabled:    cfg.MCP.Server.Enabled,
				MCPTransport:  mcpTransport,
			})
		},
	}
}

func restartCmd(gf *globalFlags) *cobra.Command {
	var skipPreflight, preflightFull bool
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the background OpsIntelligence process",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = stopCmd(gf).RunE(cmd, args)
			time.Sleep(1 * time.Second)
			return execStart(gf, false, skipPreflight, preflightFull, cmd)
		},
	}
	registerPreflightFlags(cmd, &skipPreflight, &preflightFull)
	return cmd
}

// ─────────────────────────────────────────────
// providers command
// ─────────────────────────────────────────────

func providersCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "providers", Short: "List and manage LLM providers"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all configured LLM providers and their models",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			reg := provider.NewRegistry()
			registerProviders(ctx, cfg, reg, log) //nolint:errcheck
			for _, p := range reg.All() {
				models, err := p.ListModels(ctx)
				suffix := ""
				if err != nil {
					suffix = " (error: " + err.Error() + ")"
				}
				fmt.Printf("\n%s%s\n", p.Name(), suffix)
				for _, m := range models {
					local := ""
					if m.Local {
						local = " [local]"
					}
					fmt.Printf("  - %s%s\n", m.ID, local)
				}
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "health",
		Short: "Check connectivity to all configured providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			reg := provider.NewRegistry()
			registerProviders(ctx, cfg, reg, log) //nolint:errcheck
			report := reg.CheckAll(ctx)
			for name, result := range report.Results {
				status := "✓"
				detail := ""
				if !result.OK {
					status = "✗"
					detail = " — " + result.Error
				}
				fmt.Printf("%s %s%s\n", status, name, detail)
			}
			return nil
		},
	})
	return cmd
}

// ─────────────────────────────────────────────
// embeddings command
// ─────────────────────────────────────────────

func embeddingsCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "embed", Short: "Embed text using configured embedding models"}
	cmd.AddCommand(&cobra.Command{
		Use:   "text [text]",
		Short: "Embed a piece of text and show the vector dimensions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			reg := embeddings.NewRegistry()
			registerEmbedders(ctx, cfg, reg, log)
			vec, err := reg.EmbedText(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Embedded %d-dimensional vector (showing first 8 dims): %v...\n", len(vec), vec[:min(8, len(vec))])
			return nil
		},
	})
	return cmd
}

// ─────────────────────────────────────────────
// memory command
// ─────────────────────────────────────────────

func memoryCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "memory", Short: "Search and manage conversation memory"}
	cmd.AddCommand(&cobra.Command{
		Use:   "search [query]",
		Short: "Full-text search of conversation history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			epMem, err := memory.NewEpisodicMemory(cfg.Memory.EpisodicDBPath)
			if err != nil {
				return err
			}
			defer epMem.Close()
			results, err := epMem.Search(ctx, args[0], 20)
			if err != nil {
				return err
			}
			for _, m := range results {
				fmt.Printf("[%s] %s: %s\n\n", m.CreatedAt.Format("2006-01-02 15:04"), m.Role, m.Content)
			}
			return nil
		},
	})
	mineCmd := &cobra.Command{Use: "mine", Short: "Mine/backfill taxonomy metadata for memory files"}
	var mineJSON bool
	var mineDryRun bool
	var mineMode string
	var mineLimit int
	var mineYes bool
	mineCmd.PersistentFlags().BoolVar(&mineJSON, "json", false, "Output JSON")
	mineCmd.PersistentFlags().BoolVar(&mineDryRun, "dry-run", false, "Plan run without indexing writes")
	mineCmd.PersistentFlags().StringVar(&mineMode, "mode", "", "Mining mode override: incremental|full")
	mineCmd.PersistentFlags().IntVar(&mineLimit, "limit", 0, "Maximum files to process")

	runMine := func(forceMode string) (*memory.MiningReport, error) {
		ctx := context.Background()
		log := buildLogger(gf.logLevel)
		cfg, err := loadConfig(gf.configPath, log)
		if err != nil {
			return nil, err
		}
		embedReg := embeddings.NewRegistry()
		registerEmbedders(ctx, cfg, embedReg, log)
		memMgr, err := memory.NewManager(memory.ManagerConfig{
			WorkingTokenBudget:  cfg.Memory.WorkingTokenBudget,
			EpisodicDBPath:      cfg.Memory.EpisodicDBPath,
			SemanticDBPath:      cfg.Memory.SemanticDBPath,
			EmbeddingDimensions: 1536,
			ChunkSize:           cfg.Memory.Mining.ChunkSize,
			ChunkOverlap:        cfg.Memory.Mining.ChunkOverlap,
		})
		if err != nil {
			return nil, err
		}
		defer memMgr.Close()
		mode := cfg.Memory.Mining.Mode
		if mineMode != "" {
			mode = mineMode
		}
		if forceMode != "" {
			mode = forceMode
		}
		maxFiles := cfg.Memory.Mining.MaxFilesPerRun
		if mineLimit > 0 {
			maxFiles = mineLimit
		}
		report, err := memMgr.Mine(ctx, embedReg, cfg.StateDir, memory.MiningOptions{
			Mode:           mode,
			Include:        cfg.Memory.Mining.Include,
			Exclude:        cfg.Memory.Mining.Exclude,
			MaxFilesPerRun: maxFiles,
			MaxFileSizeKB:  cfg.Memory.Mining.MaxFileSizeKB,
			StatePath:      cfg.Memory.Mining.StatePath,
			DryRun:         mineDryRun,
		})
		if err != nil {
			return nil, err
		}
		return &report, nil
	}

	mineCmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run an incremental mining pass",
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := runMine("")
			if err != nil {
				return err
			}
			if mineJSON {
				data, _ := json.MarshalIndent(report, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			fmt.Printf("memory mine run complete: %s\n", report.PrettyString())
			return nil
		},
	})
	mineCmd.AddCommand(&cobra.Command{
		Use:   "backfill",
		Short: "Run a full backfill over all included files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !mineYes {
				return fmt.Errorf("backfill requires --yes")
			}
			report, err := runMine("full")
			if err != nil {
				return err
			}
			if mineJSON {
				data, _ := json.MarshalIndent(report, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			fmt.Printf("memory mine backfill complete: %s\n", report.PrettyString())
			return nil
		},
	})
	mineCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show last mining run status",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			report, err := memory.ReadMiningState(cfg.Memory.Mining.StatePath)
			if err != nil {
				return err
			}
			if mineJSON {
				data, _ := json.MarshalIndent(report, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			fmt.Printf("memory mine status: %s\n", report.PrettyString())
			return nil
		},
	})
	mineCmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate mining config and embedder readiness",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			embedReg := embeddings.NewRegistry()
			registerEmbedders(context.Background(), cfg, embedReg, log)
			if _, ok := embedReg.Default(); !ok {
				return fmt.Errorf("no embedding provider available for mining")
			}
			if mineJSON {
				fmt.Println(`{"schema_version":1,"valid":true}`)
				return nil
			}
			fmt.Println("memory mine validate: ok")
			return nil
		},
	})
	mineCmd.PersistentFlags().BoolVar(&mineYes, "yes", false, "Confirm destructive/full backfill operations")
	cmd.AddCommand(mineCmd)
	return cmd
}

// ─────────────────────────────────────────────
// tools command
// ─────────────────────────────────────────────

func toolsCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "tools", Short: "List all tools available to the agent"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all tools: built-in, skill, and auto-generated",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			path := gf.configPath
			if path == "" {
				path = config.DefaultConfigPath()
			}
			cfg, err := loadConfig(path, log)
			if err != nil {
				return err
			}

			prim := lipgloss.NewStyle().Foreground(tui.ColorPrimary).Bold(true)
			dim := lipgloss.NewStyle().Foreground(tui.ColorMuted)
			header := lipgloss.NewStyle().Foreground(tui.ColorNeon).Bold(true)

			// ── Section 1: Built-in tools ──────────────────────────────────────
			fmt.Println(header.Render("\n⚡ Built-in System Tools") + dim.Render("  (always available)"))
			fmt.Println(dim.Render("─────────────────────────────────────────────────────────────"))
			builtins := []struct{ name, desc string }{
				{"bash", "Execute any shell command (git, gh, npm, pip, compile, run tests…)"},
				{"write_file", "Create or overwrite any file — source code, configs, scripts"},
				{"read_file", "Read file contents (with optional line range)"},
				{"list_dir", "Browse directory contents, optionally recursive"},
				{"grep", "Search patterns across files (regex, case-insensitive modes)"},
				{"web_fetch", "Fetch text content from a URL (docs, APIs, READMEs)"},
				{"memory_search", "Search episodic + semantic (vector) memory"},
				{"memory_get", "Read a specific line range from a memory file"},
				{"message", "Send a message to a configured channel (e.g. Slack) by session_id"},
				{"cron", "Schedule / list / cancel recurring agent jobs"},
				{"chain_run", "Run a named smart-prompt chain (pr-review, sonar-triage, cicd-regression, incident-scribe) or a single meta prompt"},
				{"chain_list", "List available smart-prompt chains + meta prompts"},
				{"subagent_create", "Register a named specialist sub-agent (workspace + SOUL.md)"},
				{"subagent_list", "List registered sub-agents"},
				{"subagent_run", "Run a blocking task on a sub-agent; returns its final text"},
				{"subagent_run_async", "Dispatch a sub-agent task in the background; returns a task_id"},
				{"subagent_run_parallel", "Fan out N sub-agent tasks concurrently and wait for all"},
				{"subagent_status", "Return status/result of an async sub-agent task"},
				{"subagent_wait", "Block until listed async task_ids are terminal (with timeout)"},
				{"subagent_tasks", "List recent async sub-agent tasks (status + elapsed)"},
				{"subagent_cancel", "Cancel a pending or running async sub-agent task"},
				{"subagent_intervene", "Push authoritative guidance into a running sub-agent (applied on its next turn)"},
				{"subagent_stream", "Drain the progress-event stream for a task (or all active tasks)"},
				{"subagent_share_context", "Record an explicit context-share note in a sub-agent task's audit log"},
				{"subagent_read_context", "Read back the shared-context audit trail for a task"},
				{"subagent_remove", "Unregister a sub-agent (and delete workspace by default)"},
				{"devops.github.list_prs", "List open PRs for an owner/repo"},
				{"devops.github.pr_diff", "Fetch a PR's unified diff + metadata"},
				{"devops.github.workflow_runs", "List recent GitHub Actions runs (status, conclusion, head_sha)"},
				{"devops.github.commit_status", "Check combined commit status for a ref"},
				{"devops.gitlab.list_mrs", "List open GitLab merge requests"},
				{"devops.gitlab.pipelines", "List recent GitLab pipelines"},
				{"devops.jenkins.job_status", "Query a Jenkins job's last build status"},
				{"devops.sonar.quality_gate", "Read the current SonarQube quality gate"},
				{"devops.sonar.issues", "List Sonar issues (optionally new-code-only)"},
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, t := range builtins {
				fmt.Fprintf(w, "  %s\t%s\n", prim.Render(t.name), dim.Render(t.desc))
			}
			w.Flush()

			// ── Section 2: Skill tools ─────────────────────────────────────────
			customDir := filepath.Join(cfg.StateDir, "skills", "custom")
			skillReg := skills.NewRegistry()
			_ = skillReg.LoadAll(context.Background(), customDir)
			allSkills := skillReg.List()

			var skillToolCount int
			for _, s := range allSkills {
				skillToolCount += len(s.Tools)
			}

			fmt.Println(header.Render("\n🧠 Skill Tools") + dim.Render(fmt.Sprintf("  (%d installed skills)", len(allSkills))))
			fmt.Println(dim.Render("─────────────────────────────────────────────────────────────"))
			if skillToolCount == 0 {
				fmt.Println(dim.Render("  No skill tools installed yet."))
				fmt.Println(dim.Render("  Run: opsintelligence skills install <name>"))
			} else {
				w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, s := range allSkills {
					for _, t := range s.Tools {
						fmt.Fprintf(w2, "  %s\t%s\t%s\n",
							prim.Render(t.Name),
							dim.Render("["+s.Name+"]"),
							dim.Render(t.Description))
					}
				}
				w2.Flush()
			}

			// ── Section 3: Auto-generated tools ───────────────────────────────
			creator, err := autotool.NewCreator(autotool.CreatorConfig{
				ToolsDir: cfg.Agent.ToolsDir,
				VenvPath: filepath.Join(cfg.StateDir, "venv"),
				Timeout:  30,
			}, log)
			var autoList []autotool.ToolMeta
			if err == nil {
				autoList, _ = creator.List()
			}

			fmt.Println(header.Render("\n🔧 Auto-generated Tools") + dim.Render(fmt.Sprintf("  (%d generated)", len(autoList))))
			fmt.Println(dim.Render("─────────────────────────────────────────────────────────────"))
			if len(autoList) == 0 {
				fmt.Println(dim.Render("  No auto-generated tools yet."))
				fmt.Println(dim.Render("  Ask the agent to create one — it uses 'bash' and 'write_file' automatically."))
			} else {
				w3 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, t := range autoList {
					fmt.Fprintf(w3, "  %s\t%s\t%s\n",
						prim.Render(t.Name),
						dim.Render(t.CreatedAt.Format("2006-01-02")),
						dim.Render(t.Description))
				}
				w3.Flush()
			}

			fmt.Println()
			return nil
		},
	})
	return cmd
}

func extensionsCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extensions",
		Short: "Extension hooks and built-in equivalents (OpsIntelligence)",
		Long: `OpsIntelligence does not load third-party Node extension bundles. Built-in coverage:

  • Channels, skills, MCP clients, webhooks, cron, browser tools, voice — see list.
  • Optional prompt_files in opsintelligence.yaml merge markdown into the system prompt
    (workspace prompt fragments as plain markdown, not executable plugins).

Extend behavior via skills, MCP, channels, or prompt_files as needed.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Summarize extension-equivalent features and extensions.* config",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			prim := lipgloss.NewStyle().Foreground(tui.ColorPrimary).Bold(true)
			dim := lipgloss.NewStyle().Foreground(tui.ColorMuted)

			fmt.Println(prim.Render("Channels (in-process, not npm plugins)"))
			var ch []string
			if cfg.Channels.Slack != nil {
				ch = append(ch, "slack")
			}
			if len(ch) == 0 {
				fmt.Println(dim.Render("  (none configured)"))
			} else {
				fmt.Println(dim.Render("  " + strings.Join(ch, ", ")))
			}

			fmt.Println(prim.Render("\nMCP"))
			fmt.Printf("  server enabled: %v  transport: %s\n", cfg.MCP.Server.Enabled, cfg.MCP.Server.Transport)
			fmt.Printf("  external clients: %d\n", len(cfg.MCP.Clients))

			fmt.Println(prim.Render("\nWebhooks"))
			if cfg.Webhooks.Enabled {
				fmt.Printf("  enabled, mappings: %d\n", len(cfg.Webhooks.Mappings))
			} else {
				fmt.Println(dim.Render("  disabled"))
			}

			fmt.Println(prim.Render("\nCron"))
			fmt.Printf("  jobs: %d\n", len(cfg.Cron))

			fmt.Println(prim.Render("\nSkills"))
			fmt.Printf("  enabled in config: %d\n", len(cfg.Agent.EnabledSkills))

			fmt.Println(prim.Render("\nVoice / browser / memory"))
			fmt.Println(dim.Render("  voice: STT/TTS via internal/voice (yaml voice:)"))
			fmt.Println(dim.Render("  browser: browser_navigate, browser_screenshot (chromedp)"))
			fmt.Println(dim.Render("  memory: working + episodic.db + semantic (embeddings); optional MemPalace via mcp / memory.mempalace (managed_venv automates pip + init)"))

			fmt.Println(prim.Render("\nextensions.prompt_files (extra markdown prompt fragments)"))
			if !cfg.Extensions.Enabled {
				fmt.Println(dim.Render("  disabled (set extensions.enabled: true)"))
			} else if len(cfg.Extensions.PromptFiles) == 0 {
				fmt.Println(dim.Render("  enabled, no files listed"))
			} else {
				for _, p := range cfg.Extensions.PromptFiles {
					fmt.Printf("  • %s\n", p)
				}
			}
			fmt.Println()
			return nil
		},
	})
	return cmd
}

// ─────────────────────────────────────────────
// gateway command
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
// gateway command (start · stop · restart · serve)
// ─────────────────────────────────────────────

func gatewayCmd(gf *globalFlags) *cobra.Command {
	var skipPreflight, preflightFull bool
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Manage the OpsIntelligence Gateway and Web UI",
		Long: `Manage the OpsIntelligence background gateway and embedded web UI.

Subcommands:
  start    Start in background daemon mode (web UI + channels)
  stop     Stop the running background daemon
  restart  Restart the background daemon
  serve    Run the gateway in the foreground (blocks terminal)
  status   Show daemon status (alias of 'opsintelligence status')

By default, start and serve run a fast preflight (doctor subset, --skip-network) before binding. Use --preflight-full for full network checks.`,
	}
	registerGatewayPreflightFlags(cmd, &skipPreflight, &preflightFull)

	// gateway start — alias of 'opsintelligence start --daemon'
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start OpsIntelligence daemon in background (web UI + agent + channels)",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			defer log.Sync() //nolint:errcheck
			pctx, cancel := context.WithTimeout(context.Background(), preflightDefaultTimeout)
			defer cancel()
			if err := runPreflight(pctx, gf, preflightOpts{Skip: skipPreflight, Full: preflightFull}, log, cmd.ErrOrStderr()); err != nil {
				return err
			}
			return Detach("start")
		},
	})

	// gateway stop — alias of 'opsintelligence stop'
	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the running OpsIntelligence background daemon",
		RunE:  stopCmd(gf).RunE,
	})

	// gateway restart — alias of 'opsintelligence restart'
	cmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart the OpsIntelligence background daemon",
		RunE:  restartCmd(gf).RunE,
	})

	// gateway status — alias of 'opsintelligence status'
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show OpsIntelligence daemon status, PID, and web UI address",
		RunE:  statusCmd(gf).RunE,
	})

	// gateway serve — foreground gateway-only server (dev/debug)
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the gateway in the foreground (blocks terminal)",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			defer log.Sync() //nolint:errcheck

			pctx, cancel := context.WithTimeout(context.Background(), preflightDefaultTimeout)
			defer cancel()
			if err := runPreflight(pctx, gf, preflightOpts{Skip: skipPreflight, Full: preflightFull}, log, cmd.ErrOrStderr()); err != nil {
				return err
			}

			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}

			log.Info("Starting OpsIntelligence Gateway (foreground)...",
				zap.String("host", cfg.Gateway.Host),
				zap.Int("port", cfg.Gateway.Port),
				zap.String("bind", cfg.Gateway.Bind),
			)
			srv := gateway.NewServer(cfg.Gateway.Port)
			srv.Bind = cfg.Gateway.Bind
			srv.Tailscale.Mode = cfg.Gateway.Tailscale.Mode
			srv.Token = cfg.Gateway.Token
			srv.Version = version
			srv.Config = cfg
			srv.Logger = log
			if waReg, err := buildWebhookAdapterRegistry(cfg, log); err != nil {
				return err
			} else if waReg != nil {
				srv.WebhookAdapters = waReg
			}

			authCtx, authCancel := context.WithTimeout(context.Background(), 30*time.Second)
			storeCloser, err := attachAuthToGateway(authCtx, cfg, gf.configPath, log, srv)
			authCancel()
			if err != nil {
				return err
			}
			if storeCloser != nil {
				defer func() {
					if err := storeCloser(); err != nil {
						log.Warn("gateway datastore close", zap.Error(err))
					}
				}()
			}

			if cfg.Gmail.Enabled {
				srv.Gmail = automation.NewGmailWatcher(cfg.Gmail, log)
			}
			if cfg.Voice.Enabled {
				srv.Voice = voice.NewDaemon(cfg.Voice)
			}

			webHost := cfg.Gateway.Host
			if webHost == "" {
				webHost = "localhost"
			}
			fmt.Printf("\n🌐 Web UI: http://%s:%d\n", webHost, cfg.Gateway.Port)

			errCh := make(chan error, 1)
			go func() {
				if err := srv.Start(); err != nil && err != http.ErrServerClosed {
					errCh <- err
				}
			}()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

			select {
			case err := <-errCh:
				return fmt.Errorf("gateway error: %w", err)
			case <-sigCh:
				log.Info("Shutting down Gateway...")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := srv.Stop(ctx); err != nil {
					return fmt.Errorf("shutdown error: %w", err)
				}
			}
			return nil
		},
	})

	return cmd
}

// ─────────────────────────────────────────────
// version command
// ─────────────────────────────────────────────

func versionCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			tui.MaybePrintVersion(version, gf.noColor)
		},
	}
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

// effectiveMCPClients returns cfg.MCP.Clients plus an optional synthetic MemPalace stdio client
// when memory.mempalace.auto_start is true and no client with the same name is already defined.
func effectiveMCPClients(cfg *config.Config) ([]config.MCPClientConfig, bool) {
	out := append([]config.MCPClientConfig(nil), cfg.MCP.Clients...)
	name := strings.TrimSpace(cfg.Memory.MemPalace.MCPClientName)
	if name == "" {
		name = "mempalace"
	}
	if !cfg.Memory.MemPalace.AutoStart {
		return out, false
	}
	for _, c := range out {
		if c.Name == name {
			return out, false
		}
	}
	py := strings.TrimSpace(cfg.Memory.MemPalace.PythonExecutable)
	if py == "" {
		py = "python3"
	}
	syn := config.MCPClientConfig{
		Name:      name,
		Transport: "stdio",
		Command:   py,
		Args:      []string{"-m", "mempalace.mcp_server"},
	}
	if cfg.Memory.MemPalace.ManagedVenv {
		syn.Dir = mempalace.ManagedWorldDir(cfg.StateDir)
	}
	out = append(out, syn)
	return out, true
}

// augmentActiveSkillsWithMCP appends skill names registered by external MCP (prefix "mcp:")
// so they appear in the session skills header and skill_graph_index without requiring users
// to list every server in agent.enabled_skills.
func augmentActiveSkillsWithMCP(skillReg skills.Registry, active []string) []string {
	seen := make(map[string]struct{}, len(active))
	for _, n := range active {
		if n == "" {
			continue
		}
		seen[n] = struct{}{}
	}
	out := append([]string(nil), active...)
	for _, s := range skillReg.List() {
		name := s.Name
		if !strings.HasPrefix(name, "mcp:") {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// buildWebhookAdapterRegistry builds a webhookadapter.Registry from the
// opsintelligence.yaml webhooks.adapters block. Adapters that are
// disabled (or whose config is obviously incomplete — e.g. missing
// secret) are omitted; registration errors cause startup to fail loudly
// (we never silently accept traffic on a misconfigured webhook).
//
// Add a new adapter here when you wire a new package under
// internal/webhookadapter/<name>/. The pattern is the same for every
// provider: read its typed config, skip if !Enabled, register.
func buildWebhookAdapterRegistry(cfg *config.Config, log *zap.Logger) (*webhookadapter.Registry, error) {
	if cfg == nil || !cfg.Webhooks.Enabled {
		return nil, nil
	}
	reg := webhookadapter.NewRegistry()

	if ghCfg := cfg.Webhooks.Adapters.GitHub; ghCfg.Enabled {
		if strings.TrimSpace(ghCfg.Secret) == "" && !ghCfg.AllowUnverified {
			return nil, fmt.Errorf("webhooks.adapters.github: secret is required when allow_unverified is false")
		}
		adapter := ghadapter.New(ghadapter.Config{
			Enabled:         ghCfg.Enabled,
			Secret:          ghCfg.Secret,
			Path:            ghCfg.Path,
			Default:         ghCfg.Default,
			Events:          ghCfg.Events,
			Prompts:         ghCfg.Prompts,
			AllowUnverified: ghCfg.AllowUnverified,
		})
		if err := reg.Register(adapter); err != nil {
			return nil, fmt.Errorf("webhooks.adapters.github: %w", err)
		}
		if log != nil {
			log.Info("webhook adapter registered",
				zap.String("adapter", adapter.Name()),
				zap.String("path", "/api/webhook/"+adapter.Path()),
				zap.Bool("allow_unverified", ghCfg.AllowUnverified),
			)
		}
	}

	// When no adapter registered, return nil so the gateway falls back to
	// legacy mappings only.
	if len(reg.List()) == 0 {
		return nil, nil
	}
	return reg, nil
}

func mcpClientConfigsFromYAML(in []config.MCPClientConfig) []mcp.ClientConfig {
	out := make([]mcp.ClientConfig, 0, len(in))
	for _, c := range in {
		tr := mcp.TransportStdio
		if strings.EqualFold(strings.TrimSpace(c.Transport), "http") {
			tr = mcp.TransportHTTP
		}
		out = append(out, mcp.ClientConfig{
			Name:      c.Name,
			Transport: tr,
			Command:   c.Command,
			Args:      c.Args,
			Dir:       c.Dir,
			Env:       c.Env,
			URL:       c.URL,
			AuthToken: c.AuthToken,
		})
	}
	return out
}

func loadConfig(path string, log *zap.Logger) (*config.Config, error) {
	if path == "" {
		path = config.DefaultConfigPath()
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, err := runOnboarding(path); err != nil {
			log.Warn("interactive onboarding failed or was skipped, falling back to environment variables", zap.Error(err))
			return config.LoadFromEnv(), nil
		}
	}
	return config.Load(path)
}

func runAgent(gf *globalFlags, configPath string, model string, message string, sessionID string, serve bool, noStream bool, auto bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := buildLogger(gf.logLevel)
	defer log.Sync() //nolint:errcheck

	cfg, err := loadConfig(configPath, log)
	if err != nil {
		return err
	}

	shutdownTracing := obstracing.Init(ctx, obstracing.Config{
		Enabled:     cfg.Tracing.Enabled,
		Endpoint:    cfg.Tracing.OTLPEndpoint,
		ServiceName: cfg.Tracing.ServiceName,
		SampleRatio: cfg.Tracing.SampleRatio,
	}, log)
	defer func() {
		_ = shutdownTracing(context.Background())
	}()

	// Seed workspace identity files (SOUL.md, IDENTITY.md, AGENTS.md, etc.)
	// Use cfg.StateDir (already resolved to ~/.opsintelligence) not the raw configPath
	// flag which can be an empty string when no --config flag is passed.
	resolvedConfigPath := filepath.Join(cfg.StateDir, "opsintelligence.yaml")
	if wsErr := config.InitializeWorkspace(resolvedConfigPath); wsErr != nil {
		log.Warn("workspace init failed", zap.Error(wsErr))
	}

	// Boot all subsystems
	reg := provider.NewRegistry()
	if err := registerProviders(ctx, cfg, reg, log); err != nil {
		log.Warn("some providers failed to register", zap.Error(err))
	}

	embedReg := embeddings.NewRegistry()
	registerEmbedders(ctx, cfg, embedReg, log)

	// Ensure memory dirs exist
	if err := os.MkdirAll(filepath.Dir(cfg.Memory.EpisodicDBPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Memory.SemanticDBPath), 0o755); err != nil {
		return err
	}
	dims := 1536 // default for OpenAI small
	if e, ok := embedReg.Default(); ok {
		models, _ := e.ListModels(ctx)
		if len(models) > 0 {
			dims = models[0].Dimensions
		}
	}
	memMgr, err := memory.NewManager(memory.ManagerConfig{
		WorkingTokenBudget:  cfg.Memory.WorkingTokenBudget,
		EpisodicDBPath:      cfg.Memory.EpisodicDBPath,
		SemanticDBPath:      cfg.Memory.SemanticDBPath,
		EmbeddingDimensions: dims,
		ChunkSize:           cfg.Memory.Mining.ChunkSize,
		ChunkOverlap:        cfg.Memory.Mining.ChunkOverlap,
	})
	if err != nil {
		return fmt.Errorf("memory init: %w", err)
	}
	defer memMgr.Close()

	// Start Markdown memory watcher/synchronizer
	go func() {
		if err := memMgr.Watch(ctx, embedReg, cfg.StateDir); err != nil {
			log.Warn("memory watcher failed", zap.Error(err))
		}
	}()

	// Resolve model
	resolvedModel := model
	if resolvedModel == "" {
		resolvedModel = cfg.Routing.Default
	}
	if resolvedModel == "" {
		// Auto-select first available provider
		for _, p := range reg.All() {
			models, _ := p.ListModels(ctx)
			if len(models) > 0 {
				resolvedModel = p.Name() + "/" + models[0].ID
				break
			}
		}
	}
	if resolvedModel == "" {
		return fmt.Errorf("no model configured — set routing.default in config or use --model")
	}

	p, modelInfo, err := reg.ResolveModel(ctx, resolvedModel)
	if err != nil {
		return fmt.Errorf("resolve model %q: %w", resolvedModel, err)
	}
	log.Info("using model", zap.String("model", modelInfo.ID), zap.String("provider", p.Name()))

	// Extract bundled skills on first run
	bundledDir := filepath.Join(cfg.StateDir, "skills", "bundled")
	customDir := filepath.Join(cfg.StateDir, "skills", "custom")
	if err := extractBundledSkills(bundledDir); err != nil {
		log.Warn("failed to extract bundled skills", zap.Error(err))
	}

	// Load Skills — respects enabled_skills if set, else loads all from custom dir
	skillReg := skills.NewRegistry()
	if err := skillReg.LoadAll(ctx, customDir); err != nil {
		log.Warn("failed to load skills", zap.Error(err))
	}

	// Determine active skill names
	activeSkillNames := cfg.Agent.EnabledSkills
	if len(activeSkillNames) == 0 {
		// No explicit list = all installed custom skills are active
		for _, s := range skillReg.List() {
			activeSkillNames = append(activeSkillNames, s.Name)
		}
	}

	// Build tool registry
	toolReg := agent.NewToolRegistry()

	// Register tools from skills
	for _, s := range skillReg.List() {
		skillTools := skills.ConvertTools(&s, cfg.Agent.SkillsDir) // Simplification: assuming all tools relative to skills dir
		for _, t := range skillTools {
			toolReg.Register(t)
		}
	}

	if cfg.Memory.MemPalace.ManagedVenv && cfg.Memory.MemPalace.AutoStart {
		log.Info("mempalace: ensuring managed venv (first run may download PyPI packages)")
		if err := mempalace.Ensure(ctx, mempalace.EnsureOptions{
			StateDir:        cfg.StateDir,
			BootstrapPython: cfg.Memory.MemPalace.BootstrapPython,
			Progress:        os.Stderr,
			Log:             log,
		}); err != nil {
			return fmt.Errorf("mempalace managed venv: %w", err)
		}
	}

	mcpClientList, mempalaceAuto := effectiveMCPClients(cfg)
	if mempalaceAuto {
		msg := "mcp: MemPalace auto-start (stdio child process)"
		if cfg.Memory.MemPalace.ManagedVenv {
			msg = "mcp: MemPalace auto-start (managed venv + stdio child)"
		}
		log.Info(msg,
			zap.String("client", strings.TrimSpace(cfg.Memory.MemPalace.MCPClientName)),
			zap.String("python", strings.TrimSpace(cfg.Memory.MemPalace.PythonExecutable)),
		)
	}
	mcpCfgs := mcpClientConfigsFromYAML(mcpClientList)
	var externalMCP []*mcp.Client
	if len(mcpCfgs) > 0 {
		externalMCP = mcp.RegisterExternalMCPTools(ctx, mcpCfgs, skillReg, toolReg, nil, log)
	}
	defer func() {
		for i := len(externalMCP) - 1; i >= 0; i-- {
			_ = externalMCP[i].Close()
		}
	}()

	// Virtual MCP skills are registered after the initial activeSkillNames pass; include them so
	// BuildContext, skill_graph_index, and read_skill_node see custom MCP servers alongside disk skills.
	activeSkillNames = augmentActiveSkillsWithMCP(skillReg, activeSkillNames)
	skillsCtx := skillReg.BuildContext(activeSkillNames)

	memSearchFn := func(searchCtx context.Context, query string, limit int) ([]string, error) {
		var out []string
		router := memory.NewHeuristicPalaceRouter()
		route := router.Route(query)
		shouldRoute := cfg.Agent.Palace.Enabled && !cfg.Agent.Palace.ShadowOnly && cfg.Agent.Palace.MemorySearchRouting

		// 1. Episodic Search (Full-Text)
		msgs, err := memMgr.Episodic.Search(searchCtx, query, limit)
		if err == nil {
			for _, m := range msgs {
				out = append(out, fmt.Sprintf("[episodic] [%s] %s: %s", m.CreatedAt.Format("2006-01-02 15:04"), m.Role, m.Content))
			}
		}

		// 2. Semantic Search (Vector)
		if vec, err := embedReg.EmbedQuery(searchCtx, query); err == nil {
			docs, err := memMgr.Semantic.SearchWithModel(searchCtx, vec, limit)
			if err == nil {
				if shouldRoute && !route.IsZero() {
					filtered := make([]memory.Document, 0, len(docs))
					for _, d := range docs {
						if route.MatchesDocument(d) {
							filtered = append(filtered, d)
						}
					}
					if len(filtered) > 0 {
						docs = filtered
					} else if !cfg.Agent.Palace.FailOpen {
						docs = nil
					}
				}
				var docPaths []string
				for _, d := range docs {
					docPaths = append(docPaths, d.Source)
					out = append(out, fmt.Sprintf("[semantic] [score:%.2f] [%s / %s] source=%s taxonomy=%s/%s/%s: %s", d.Score, d.Model, d.CreatedAt.Format("2006-01-02 15:04"), d.Source, d.Palace, d.Wing, d.Room, d.Content))
				}

				// QueryWeaver Logic: Discover bridges between matched skill nodes
				if bridges := skillReg.FindBridges(docPaths); len(bridges) > 0 {
					for _, b := range bridges {
						out = append(out, fmt.Sprintf("[semantic] [bridge] source=%s: %s", b.FilePath, b.Instructions))
					}
				}
			}
		}

		if cfg.Memory.MemPalace.Enabled && cfg.Memory.MemPalace.InjectIntoMemorySearch {
			clientName := strings.TrimSpace(cfg.Memory.MemPalace.MCPClientName)
			if clientName == "" {
				clientName = "mempalace"
			}
			toolName := "mcp:" + clientName + ":mempalace_search"
			if t, ok := toolReg.Get(toolName); ok {
				mpLimit := limit
				if cfg.Memory.MemPalace.SearchLimit > 0 {
					mpLimit = cfg.Memory.MemPalace.SearchLimit
				}
				input, err := json.Marshal(map[string]any{
					"query": query,
					"limit": mpLimit,
				})
				if err == nil {
					if text, err := t.Execute(searchCtx, input); err != nil {
						log.Debug("mempalace_search delegate failed", zap.String("tool", toolName), zap.Error(err))
					} else if strings.TrimSpace(text) != "" {
						out = append(out, "[mempalace] "+text)
					}
				}
			} else {
				log.Warn("memory.mempalace.inject_into_memory_search is true but MCP tool is missing",
					zap.String("expected_tool", toolName),
					zap.String("hint", "enable memory.mempalace.managed_venv + auto_start, or run: opsintelligence mempalace setup"),
				)
			}
		}

		return out, nil
	}
	memSnippetFn := func(snippetCtx context.Context, source string, startLine, endLine int) (string, error) {
		// If source doesn't exist, try resolving it relative to workspace
		path := source
		if _, err := os.Stat(path); os.IsNotExist(err) {
			path = filepath.Join(cfg.StateDir, source) // cfg.StateDir is workspace dir for now
		}
		return memMgr.Semantic.GetSnippet(snippetCtx, path, startLine, endLine)
	}
	channelSenders := map[string]tools.ChannelSender{}
	for _, t := range tools.Default(memSearchFn, memSnippetFn, memMgr.Episodic, p, modelInfo.ID, channelSenders, cfg.StateDir) {
		if tool, ok := t.(agent.Tool); ok {
			toolReg.Register(tool)
		}
	}

	for _, t := range tools.DevOpsTools(cfg.DevOps) {
		toolReg.Register(t)
		log.Info("devops tool registered", zap.String("tool", t.Definition().Name))
	}

	// Smart-prompt library: merge the embedded defaults with any operator
	// overrides under <state_dir>/prompts/. Register `chain_run` and
	// `chain_list` so the LLM can invoke named chains (pr-review,
	// sonar-triage, cicd-regression, incident-scribe) and single meta
	// prompts (self-critique, evidence-extractor, plan-then-act).
	promptLibrary, promptIndex := loadSmartPrompts(cfg.StateDir, log)
	if promptLibrary != nil {
		pr := &prompts.Runner{
			Provider:     p,
			Lib:          promptLibrary,
			DefaultModel: modelInfo.ID,
		}
		toolReg.Register(tools.NewChainRunTool(pr))
		toolReg.Register(tools.NewChainListTool(pr))
		log.Info("smart prompts loaded",
			zap.Int("chains", len(promptLibrary.ListChains())),
			zap.Int("prompts", len(promptLibrary.ListPrompts())),
		)
	}
	// Build the graph-based tool catalog for per-request token-efficient selection.
	toolGraph := graph.NewToolGraph()
	catalog := tools.NewCatalog(toolReg, toolGraph)

	// Register find_tools (the Anthropic-pattern tool discovery tool).
	toolReg.Register(tools.FindToolsTool{Catalog: catalog})

	// Register skill_graph_index (on-demand skill node discovery).
	// activeSkillNames is already populated above (cfg.Agent.EnabledSkills or all loaded skills).
	toolReg.Register(&skills.SkillGraphIndexTool{
		Registry:     skillReg,
		ActiveSkills: activeSkillNames,
	})

	// Also register read_skill_node here (previously registered separately).
	toolReg.Register(skills.NewReadSkillNodeTool(skillReg))

	// Register repair_skill (auto-installation of missing dependencies).
	toolReg.Register(&skills.RepairSkillTool{Registry: skillReg})

	// Proactive self-healing: repair all enabled skills if they have missing dependencies.
	_ = skillReg.RepairAllEnabled(ctx, activeSkillNames)

	// Rebuild catalog to include all newly registered tools (find_tools, skill_graph_index, read_skill_node).
	catalog = tools.NewCatalog(toolReg, toolGraph)

	// Derive provider name for capability detection from the resolved model ID.
	// e.g. "anthropic/claude-opus-4" → "anthropic", "claude-opus-4" → "" (uses default caps)
	providerNameForCaps := ""
	if modelInfo.ID != "" {
		if idx := strings.Index(modelInfo.ID, "/"); idx > 0 {
			providerNameForCaps = strings.ToLower(modelInfo.ID[:idx])
		}
	}

	hw, _ := system.Detect(ctx)
	extPrompt := ""
	if cfg.Extensions.Enabled && len(cfg.Extensions.PromptFiles) > 0 {
		extPrompt = extensions.PromptAppendix(cfg.StateDir, cfg.Extensions.PromptFiles)
	}
	if strings.TrimSpace(promptIndex) != "" {
		if extPrompt != "" {
			extPrompt += "\n\n"
		}
		extPrompt += "## Smart Prompts Index\n\n" +
			"You have access to the curated chain_run tool. Invoke one of the ids below to offload a multi-step reasoning task (each chain is a bounded, self-critiquing pipeline). Use `chain_list` at runtime if this index is stale.\n\n" +
			promptIndex +
			"\nUsage: call chain_run with {\"id\": \"<chain or prompt id>\", \"inputs\": { ... }}. Chains return a final rendered answer and a per-step trace. Single meta prompts (prompt:*) return one string. Chains never loop and never call write-action tools."
	}
	localIntelCache := strings.TrimSpace(cfg.Agent.LocalIntel.CacheDir)
	if localIntelCache == "" {
		localIntelCache = filepath.Join(cfg.StateDir, "localintel")
	}
	runner := agent.NewRunner(agent.Config{
		MaxIterations:         cfg.Agent.MaxIterations,
		Model:                 modelInfo.ID,
		ActiveSkillsContext:   skillsCtx,
		ProviderName:          providerNameForCaps,
		ToolsProfile:          cfg.Security.Profile,
		EnablePlanning:        agentPlanningEnabled(cfg),
		EnableReflection:      agentReflectionEnabled(cfg),
		GatewayPublicBaseURL:  cfg.PublicGatewayBaseURL(),
		ExtensionPromptAppend: extPrompt,
		StateDir:              cfg.StateDir,
		LocalIntel: agent.LocalIntelRunnerConfig{
			Enabled:      cfg.Agent.LocalIntel.Enabled,
			GGUFPath:     cfg.Agent.LocalIntel.GGUFPath,
			MaxTokens:    cfg.Agent.LocalIntel.MaxTokens,
			SystemPrompt: cfg.Agent.LocalIntel.SystemPrompt,
			CacheDir:     localIntelCache,
		},
		Palace: agent.PalaceConfig{
			Enabled:             cfg.Agent.Palace.Enabled,
			ShadowOnly:          cfg.Agent.Palace.ShadowOnly,
			PromptRouting:       cfg.Agent.Palace.PromptRouting,
			MemorySearchRouting: cfg.Agent.Palace.MemorySearchRouting,
			ToolRouting:         cfg.Agent.Palace.ToolRouting,
			FailOpen:            cfg.Agent.Palace.FailOpen,
			LogDecisions:        cfg.Agent.Palace.LogDecisions,
		},
	}, p, toolReg, memMgr, log, cfg.StateDir).WithCatalog(catalog).WithHardware(hw)

	// ── Security: Guardrail + Audit Log ────────────────────────────────
	guardrailMode := security.GuardrailMode(cfg.Security.Mode)
	if guardrailMode == "" {
		guardrailMode = security.ModeMonitor
	}
	var ownerOnly []string
	if cfg.Security.OwnerOnlyPaths != nil {
		ownerOnly = *cfg.Security.OwnerOnlyPaths
	}
	guardrail, guardErr := security.NewGuardrail(guardrailMode, cfg.Security.BlockPatterns, ownerOnly)
	if guardErr != nil {
		log.Warn("security guardrail init failed", zap.Error(guardErr))
	}

	secLogPath := cfg.Security.LogPath
	if secLogPath == "" {
		secLogPath = filepath.Join(cfg.StateDir, "security", "audit.ndjson")
	}
	auditLog, auditErr := security.NewAuditLog(secLogPath, cfg.Security.PIIMask, log)
	if auditErr != nil {
		log.Warn("security audit log init failed", zap.Error(auditErr))
	}

	runner = runner.WithSecurity(guardrail, auditLog)
	log.Info("security layer active",
		zap.String("mode", string(guardrailMode)),
		zap.String("audit_log", secLogPath),
	)

	// Sub-agents (delegation): register after security so child runs inherit guardrail/audit.
	subSvc := &tools.SubAgentSvc{
		Store:                 subagents.NewStore(cfg.StateDir),
		Provider:              p,
		ParentRegistry:        toolReg,
		ToolGraph:             toolGraph,
		Mem:                   memMgr,
		Log:                   log,
		Model:                 modelInfo.ID,
		ActiveSkillsContext:   skillsCtx,
		ProviderName:          providerNameForCaps,
		GatewayPublicBaseURL:  cfg.PublicGatewayBaseURL(),
		ExtensionPromptAppend: extPrompt,
		DefaultToolsProfile:   cfg.Security.Profile,
		Guardrail:             guardrail,
		AuditLog:              auditLog,
		Hardware:              hw,
	}
	// Async task orchestration: lets the master agent dispatch multiple
	// sub-agent runs in parallel (e.g. review 3 PRs simultaneously) while
	// still reusing the same guardrail-wrapped executor as subagent_run.
	// Async task orchestration. EnsureTaskManager wires the shared
	// executor used by BOTH subagent_run (sync) and subagent_run_async
	// (background). Defaults: 8 concurrent tasks, retain last 256 in
	// memory, 30m per-task timeout.
	tasks := subSvc.EnsureTaskManager(0, 0, 0)
	toolReg.Register(tools.SubAgentCreateTool{S: subSvc})
	toolReg.Register(tools.SubAgentListTool{S: subSvc})
	toolReg.Register(tools.SubAgentRunTool{S: subSvc})
	toolReg.Register(tools.SubAgentRemoveTool{S: subSvc})
	toolReg.Register(tools.SubAgentRunAsyncTool{S: subSvc})
	toolReg.Register(tools.SubAgentRunParallelTool{S: subSvc})
	toolReg.Register(tools.SubAgentStatusTool{S: subSvc})
	toolReg.Register(tools.SubAgentWaitTool{S: subSvc})
	toolReg.Register(tools.SubAgentTasksTool{S: subSvc})
	toolReg.Register(tools.SubAgentCancelTool{S: subSvc})
	// Supervision layer: master steers running sub-agents, inspects
	// their event stream, and can explicitly share context (children
	// are otherwise fully isolated from master memory).
	toolReg.Register(tools.SubAgentInterveneTool{S: subSvc})
	toolReg.Register(tools.SubAgentStreamTool{S: subSvc})
	toolReg.Register(tools.SubAgentShareContextTool{S: subSvc})
	toolReg.Register(tools.SubAgentReadContextTool{S: subSvc})
	catalog = tools.NewCatalog(toolReg, toolGraph)
	runner = runner.WithCatalog(catalog).WithModelRegistry(reg)

	// Install the supervisor dashboard as a per-turn system-prompt
	// augmentor on the MASTER runner. The master sees a compact
	// snapshot of every active sub-agent (status, last event, pending
	// interventions) on every iteration — oversight is ambient, not
	// polled. Children have their own augmentor that drains pending
	// interventions (wired inside buildChildRunner).
	runner = runner.WithSystemPromptAugmentor(func(_ context.Context) string {
		board := tasks.Dashboard()
		if board == "" {
			return ""
		}
		return "## Active Sub-Agents (live)\n" + board
	})

	// ── Cron Daemon ───────────────────────────────────────────────────
	var cronJobs []cron.Job
	for _, j := range cfg.Cron {
		cronJobs = append(cronJobs, cron.Job{
			ID:       j.ID,
			Schedule: j.Schedule,
			Prompt:   j.Prompt,
		})
	}

	cronDaemon := cron.NewDaemon(
		cronJobs,
		runner,
		log,
		filepath.Join(cfg.StateDir, "cron_jobs.json"),
	)
	if err := cronDaemon.Start(); err != nil {
		log.Warn("failed to start cron daemon", zap.Error(err))
	} else {
		log.Info("cron daemon started", zap.Int("static_jobs", len(cronJobs)))
		defer cronDaemon.Stop()
	}

	// ─────────────────────────────────────────────────────────────

	if sessionID != "" {
		// Restore session history into working memory
		msgs, err := memMgr.Episodic.GetSession(ctx, sessionID, 200)
		if err == nil {
			wm := memMgr.GetWorking(sessionID)
			for _, m := range msgs {
				wm.Append(m)
			}
		}
	}

	// Autonomous mode
	if auto && message != "" {
		log.Info("Starting autonomous mode", zap.String("goal", message))
		fmt.Printf("\n🚀 Starting autonomous agent. Goal: %s\n\n", message)
		result, err := runner.RunAutonomous(ctx, message)
		if err != nil {
			return err
		}
		fmt.Printf("\n✅ Autonomous agent finished. Response: %s\n", result.Response)
		return nil
	}

	// Single message mode
	if message != "" {
		if noStream {
			result, err := runner.Run(ctx, memory.Message{
				ID:        uuid.New().String(),
				SessionID: sessionID, // using the sessionID derived earlier or "cli"
				Role:      memory.RoleUser,
				Content:   message,
				CreatedAt: time.Now(),
			})
			if err != nil {
				return err
			}
			fmt.Println(result.Response)
			return nil
		}
		// Streaming mode
		done := make(chan error, 1)
		runner.RunStream(ctx, memory.Message{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Role:      memory.RoleUser,
			Content:   message,
			CreatedAt: time.Now(),
		}, &cliStreamHandler{done: done})
		return <-done
	}

	var voiceClient *voice.Client
	if cfg.Voice.Enabled {
		voiceClient = voice.NewClient(cfg.Voice)
	}
	_ = voiceClient

	// Start Messaging Channels
	activeChannels := 0
	reliabilityCfg := chadapter.ReliabilityConfig{
		Retry: chadapter.RetryPolicy{
			MaxAttempts:   cfg.Channels.Outbound.MaxAttempts,
			BaseDelay:     time.Duration(cfg.Channels.Outbound.BaseDelayMS) * time.Millisecond,
			MaxDelay:      time.Duration(cfg.Channels.Outbound.MaxDelayMS) * time.Millisecond,
			JitterPercent: cfg.Channels.Outbound.JitterPercent,
		},
		Breaker: chadapter.CircuitBreakerPolicy{
			FailureThreshold: cfg.Channels.Outbound.BreakerThreshold,
			Cooldown:         time.Duration(cfg.Channels.Outbound.BreakerCooldownS) * time.Second,
		},
		DLQPath: cfg.Channels.Outbound.DLQPath,
	}
	if cfg.Channels.Slack != nil {
		sl, err := slack.New(cfg.Channels.Slack.BotToken, cfg.Channels.Slack.AppToken, cfg.Channels.Slack.DMMode, cfg.Channels.Slack.AllowFrom)
		if err == nil {
			slRS := chadapter.NewReliableSender("slack", sl, reliabilityCfg)
			sl.WithReliableOutbound(slRS)
			go sl.Start(ctx, runner.HandleChannelMessage)
			channelSenders["slack"] = reliableToolSender{
				rs: slRS,
			}
			log.Info("Slack channel active")
			activeChannels++
		}
	}

	// Heartbeats: periodic synthetic turns on a dedicated session (no chat spam).
	hb := cfg.Agent.Heartbeat
	if hb.Enabled && (serve || activeChannels > 0) {
		iv := hb.Interval
		if iv == "" {
			iv = "30m"
		}
		dur, err := time.ParseDuration(iv)
		if err != nil {
			log.Warn("heartbeat: invalid interval, using 30m", zap.String("interval", iv), zap.Error(err))
			dur = 30 * time.Minute
		}
		sid := hb.SessionID
		if sid == "" {
			sid = "opsintelligence:heartbeat"
		}
		prompt := strings.TrimSpace(hb.Prompt)
		go runHeartbeatLoop(ctx, runner, dur, sid, prompt, log)
	}

	// If --serve is active, start the Gateway (+ embedded web UI) and wait
	if serve {
		pidFile := PidFile(cfg.StateDir)
		if oldPid, err := ReadPID(pidFile); err == nil && CheckPID(oldPid) {
			return fmt.Errorf("OpsIntelligence is already running (PID: %d). Stop it first with 'opsintelligence stop'.", oldPid)
		}

		if err := WritePID(pidFile); err != nil {
			log.Warn("failed to write PID file", zap.Error(err))
		}
		defer os.Remove(pidFile)

		log.Info("Background mode active (v3 core engine)",
			zap.Bool("gateway", true),
			zap.Int("channels", activeChannels),
			zap.Int("pid", os.Getpid()),
		)

		srv := gateway.NewServer(cfg.Gateway.Port)
		srv.Bind = cfg.Gateway.Bind
		srv.Tailscale.Mode = cfg.Gateway.Tailscale.Mode
		srv.Token = cfg.Gateway.Token
		srv.Runner = runner
		srv.Version = version
		srv.Config = cfg
		srv.Logger = log
		if waReg, err := buildWebhookAdapterRegistry(cfg, log); err != nil {
			return err
		} else if waReg != nil {
			srv.WebhookAdapters = waReg
		}

		authCtx, authCancel := context.WithTimeout(context.Background(), 30*time.Second)
		storeCloser, err := attachAuthToGateway(authCtx, cfg, gf.configPath, log, srv)
		authCancel()
		if err != nil {
			return err
		}
		if storeCloser != nil {
			defer func() {
				if err := storeCloser(); err != nil {
					log.Warn("gateway datastore close", zap.Error(err))
				}
			}()
		}

		// Determine public-facing address for the web UI
		webHost := cfg.Gateway.Host
		if webHost == "" {
			webHost = "localhost"
		}
		webURL := fmt.Sprintf("http://%s:%d", webHost, cfg.Gateway.Port)
		fmt.Printf("\n🌐 Web UI: %s\n", webURL)
		fmt.Printf("   Token: %s\n\n", cfg.Gateway.Token)

		go func() {
			if err := srv.Start(); err != nil && err != http.ErrServerClosed {
				log.Error("gateway failure", zap.Error(err))
			}
		}()

		// Wait for shutdown signal
		<-ctx.Done()
		log.Info("Shutting down background service...")

		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Stop(stopCtx); err != nil {
			log.Warn("gateway shutdown error", zap.Error(err))
		}
		return nil
	}

	// Interactive REPL mode
	return runREPL(ctx, runner, log)
}

// extractBundledSkills copies the repo's skills/ directory into destDir (bundled dir).
// It searches for the skills directory relative to the binary, CWD, or common install paths.
func extractBundledSkills(destDir string) error {
	// Check if already populated (skip to avoid overwriting user edits)
	if info, err := os.ReadDir(destDir); err == nil && len(info) > 0 {
		return nil // already extracted
	}

	// Find the source skills directory
	src := resolveBundledSkillsSrc()
	if src == "" {
		// Not available (e.g., installed without repo) — that's OK, marketplace will handle it
		return nil
	}

	return skills.CopyDir(src, destDir)
}

// resolveBundledSkillsSrc locates the bundled skills/ directory relative to common install paths.
func resolveBundledSkillsSrc() string {
	candidates := []string{}

	// 1. Relative to the running binary
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "skills"))
	}

	// 2. Relative to CWD (development mode)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "skills"))
	}

	// 3. Common install locations
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".opsintelligence", "repo", "skills"))
	}
	candidates = append(candidates,
		"/usr/local/share/opsintelligence/skills",
		"/opt/opsintelligence/skills",
	)

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

// loadSmartPrompts hydrates the smart-prompt Library from the embedded
// defaults and overlays any operator overrides under <state_dir>/prompts/.
// Failures are logged but never fatal: the agent should still boot even
// if a custom prompt file has a syntax error.
func loadSmartPrompts(stateDir string, log *zap.Logger) (*prompts.Library, string) {
	embed, err := config.EmbeddedPromptsFS()
	if err != nil {
		log.Warn("smart prompts embedded fs unavailable", zap.Error(err))
		return nil, ""
	}
	ld := prompts.Loader{
		Embedded:     embed,
		EmbeddedRoot: ".",
		Dir:          filepath.Join(stateDir, "prompts"),
	}
	lib, err := ld.Load()
	if err != nil {
		log.Warn("smart prompts load failed; falling back to embedded defaults", zap.Error(err))
		fallback, err2 := prompts.Loader{Embedded: embed, EmbeddedRoot: "."}.Load()
		if err2 != nil {
			log.Warn("smart prompts embedded load failed", zap.Error(err2))
			return nil, ""
		}
		return fallback, fallback.Index()
	}
	return lib, lib.Index()
}

func buildLogger(level string) *zap.Logger {
	lvl := zap.WarnLevel
	switch strings.ToLower(level) {
	case "debug":
		lvl = zap.DebugLevel
	case "info":
		lvl = zap.InfoLevel
	case "warn":
		lvl = zap.WarnLevel
	case "error":
		lvl = zap.ErrorLevel
	}
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.EncoderConfig.TimeKey = "t"
	cfg.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	log, _ := cfg.Build()
	return log
}

func registerProviders(ctx context.Context, cfg *config.Config, reg *provider.Registry, log *zap.Logger) error {
	prov := cfg.Providers
	register := func(p provider.Provider) {
		if err := reg.Register(ctx, p); err != nil {
			log.Warn("provider registration warning", zap.String("provider", p.Name()), zap.Error(err))
		}
	}

	if prov.OpenAI != nil {
		register(openai.New(openai.Config{
			APIKey: prov.OpenAI.APIKey, BaseURL: prov.OpenAI.BaseURL,
			DefaultModel: prov.OpenAI.DefaultModel,
		}))
	}
	if prov.AzureOpenAI != nil {
		register(openai.New(openai.Config{
			APIKey: prov.AzureOpenAI.APIKey, BaseURL: prov.AzureOpenAI.BaseURL,
			IsAzure: true, APIVersion: prov.AzureOpenAI.APIVersion,
		}))
	}
	if prov.Anthropic != nil {
		register(anthropic.New(anthropic.Config{
			APIKey: prov.Anthropic.APIKey, BaseURL: prov.Anthropic.BaseURL,
			DefaultModel: prov.Anthropic.DefaultModel,
		}))
	}
	if prov.Bedrock != nil {
		p, err := bedrock.New(bedrock.Config{
			Region: prov.Bedrock.Region, Profile: prov.Bedrock.Profile,
			AccessKeyID: prov.Bedrock.AccessKeyID, SecretAccessKey: prov.Bedrock.SecretAccessKey,
			APIKey:       prov.Bedrock.APIKey,
			DefaultModel: prov.Bedrock.DefaultModel,
		})
		if err != nil {
			log.Warn("bedrock init failed", zap.Error(err))
		} else {
			register(p)
		}
	}
	if prov.Ollama != nil {
		register(ollama.New(ollama.Config{
			BaseURL: prov.Ollama.BaseURL, DefaultModel: prov.Ollama.DefaultModel,
		}))
	}
	if prov.VLLM != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "vllm", BaseURL: prov.VLLM.BaseURL, APIKey: prov.VLLM.APIKey,
			DefaultModel: prov.VLLM.DefaultModel, DiscoverModels: true,
		}))
	}
	if prov.LMStudio != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "lmstudio", BaseURL: prov.LMStudio.BaseURL,
			DefaultModel: prov.LMStudio.DefaultModel, DiscoverModels: true,
		}))
	}
	if prov.Groq != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "groq", BaseURL: "https://api.groq.com", APIKey: prov.Groq.APIKey,
			DefaultModel:   prov.Groq.DefaultModel,
			StaticModels:   catalogs.GroqModels("groq"),
			DiscoverModels: true,
		}))
	}
	if prov.Mistral != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "mistral", BaseURL: "https://api.mistral.ai", APIKey: prov.Mistral.APIKey,
			DefaultModel:   prov.Mistral.DefaultModel,
			StaticModels:   catalogs.MistralModels("mistral"),
			DiscoverModels: true,
		}))
	}
	if prov.Together != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "together", BaseURL: "https://api.together.xyz", APIKey: prov.Together.APIKey,
			DefaultModel:   prov.Together.DefaultModel,
			StaticModels:   catalogs.TogetherModels("together"),
			DiscoverModels: true,
		}))
	}
	if prov.OpenRouter != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "openrouter", BaseURL: "https://openrouter.ai/api", APIKey: prov.OpenRouter.APIKey,
			DefaultModel:   prov.OpenRouter.DefaultModel,
			StaticModels:   catalogs.OpenRouterModels("openrouter"),
			DiscoverModels: true,
			ExtraHeaders: map[string]string{
				"HTTP-Referer": prov.OpenRouter.SiteURL,
				"X-Title":      prov.OpenRouter.SiteName,
			},
		}))
	}
	if prov.NVIDIA != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "nvidia", BaseURL: "https://integrate.api.nvidia.com", APIKey: prov.NVIDIA.APIKey,
			DefaultModel:   prov.NVIDIA.DefaultModel,
			StaticModels:   catalogs.NVIDIAModels("nvidia"),
			DiscoverModels: true,
		}))
	}
	if prov.Cohere != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "cohere", BaseURL: "https://api.cohere.com", APIKey: prov.Cohere.APIKey,
			DefaultModel:   prov.Cohere.DefaultModel,
			StaticModels:   catalogs.CohereModels("cohere"),
			DiscoverModels: true,
		}))
	}
	if prov.HuggingFace != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "huggingface", BaseURL: prov.HuggingFace.BaseURL, APIKey: prov.HuggingFace.APIKey,
			DefaultModel: prov.HuggingFace.DefaultModel,
		}))
	}
	if prov.DeepSeek != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: prov.DeepSeek.APIKey,
			DefaultModel:   prov.DeepSeek.DefaultModel,
			StaticModels:   catalogs.DeepSeekModels("deepseek"),
			DiscoverModels: true,
		}))
	}
	if prov.Perplexity != nil {
		register(openaicompat.New(openaicompat.Config{
			Name: "perplexity", BaseURL: "https://api.perplexity.ai", APIKey: prov.Perplexity.APIKey,
			DefaultModel:   prov.Perplexity.DefaultModel,
			StaticModels:   catalogs.PerplexityModels("perplexity"),
			DiscoverModels: true,
		}))
	}
	if prov.XAI != nil {
		xaiModel := prov.XAI.DefaultModel
		if xaiModel == "" {
			xaiModel = "grok-4"
		}
		register(openaicompat.New(openaicompat.Config{
			Name:           "xai",
			BaseURL:        "https://api.x.ai/v1",
			APIKey:         prov.XAI.APIKey,
			DefaultModel:   xaiModel,
			StaticModels:   catalogs.XAIModels("xai"),
			DiscoverModels: true,
		}))
	}
	if prov.Vertex != nil {
		v, err := vertex.New(ctx, vertex.Config{
			ProjectID:    prov.Vertex.ProjectID,
			Location:     prov.Vertex.Location,
			Credentials:  prov.Vertex.Credentials,
			DefaultModel: prov.Vertex.DefaultModel,
		})
		if err != nil {
			log.Warn("vertex init failed", zap.Error(err))
		} else {
			register(v)
		}
	}
	// ─── Plano Smart Routing ───────────────────────────────────────────────────
	// If Plano is enabled, register it as the primary provider so all requests
	// flow through Plano's complexity-aware router. All other providers are still
	// registered so Plano can delegate to them, and as fallback if Plano is down.
	if cfg.Plano.Enabled {
		// Convert config.PlanoPreference → planoprovider.Preference
		prefs := make([]planoprovider.Preference, len(cfg.Plano.Preferences))
		for i, p := range cfg.Plano.Preferences {
			prefs[i] = planoprovider.Preference{
				Description: p.Description,
				PreferModel: p.PreferModel,
			}
		}

		// Look up fallback from already-registered providers
		var fallback provider.Provider
		if cfg.Plano.FallbackProvider != "" {
			if p, ok := reg.Get(cfg.Plano.FallbackProvider); ok {
				fallback = p
			}
		}

		planoP := planoprovider.New(planoprovider.Config{
			Enabled:          true,
			Endpoint:         cfg.Plano.Endpoint,
			FallbackProvider: cfg.Plano.FallbackProvider,
			Preferences:      prefs,
		}, fallback)

		register(planoP)
		log.Info("Plano smart routing enabled",
			zap.String("endpoint", cfg.Plano.Endpoint),
			zap.Int("preferences", len(prefs)),
		)
	}
	// ──────────────────────────────────────────────────────────────────────────

	return nil
}

// mergeBedrockForEmbeds fills in embeddings.bedrock from providers.bedrock when the embeddings
// block only sets default_model (or is partial). Otherwise LoadDefaultConfig falls through to
// EC2 IMDS and hangs or errors on laptops.
func mergeBedrockForEmbeds(ec *config.BedrockCreds, prov *config.BedrockCreds) *config.BedrockCreds {
	if ec == nil && prov == nil {
		return nil
	}
	var out config.BedrockCreds
	if ec != nil {
		out = *ec
	}
	if prov != nil {
		if out.Region == "" {
			out.Region = prov.Region
		}
		if out.Profile == "" {
			out.Profile = prov.Profile
		}
		if out.AccessKeyID == "" {
			out.AccessKeyID = prov.AccessKeyID
		}
		if out.SecretAccessKey == "" {
			out.SecretAccessKey = prov.SecretAccessKey
		}
		if out.APIKey == "" {
			out.APIKey = prov.APIKey
		}
		if out.DefaultModel == "" {
			out.DefaultModel = prov.DefaultModel
		}
	}
	if out.Region == "" {
		out.Region = "us-east-1"
	}
	return &out
}

func registerEmbedders(ctx context.Context, cfg *config.Config, reg *embeddings.Registry, log *zap.Logger) {
	ec := cfg.Embeddings
	register := func(e embeddings.Embedder) {
		if err := reg.Register(ctx, e); err != nil {
			log.Warn("embedder registration warning", zap.String("provider", e.Name()), zap.Error(err))
		}
	}

	// Register in priority order
	for _, name := range ec.Priority {
		switch name {
		case "openai":
			if ec.OpenAI != nil {
				register(embedproviders.NewOpenAI(ec.OpenAI.APIKey, ec.OpenAI.BaseURL))
			} else if cfg.Providers.OpenAI != nil {
				register(embedproviders.NewOpenAI(cfg.Providers.OpenAI.APIKey, ""))
			}
		case "azure":
			if ec.AzureOpenAI != nil {
				register(embedproviders.NewAzure(ec.AzureOpenAI.APIKey, ec.AzureOpenAI.BaseURL, ec.AzureOpenAI.APIVersion))
			} else if cfg.Providers.AzureOpenAI != nil {
				register(embedproviders.NewAzure(cfg.Providers.AzureOpenAI.APIKey, cfg.Providers.AzureOpenAI.BaseURL, cfg.Providers.AzureOpenAI.APIVersion))
			}
		case "ollama":
			if ec.OllamaEmbed != nil {
				register(embedproviders.NewOllama(ec.OllamaEmbed.BaseURL))
			} else if cfg.Providers.Ollama != nil {
				register(embedproviders.NewOllama(cfg.Providers.Ollama.BaseURL))
			} else {
				register(embedproviders.NewOllama(""))
			}
		case "bedrock":
			b := mergeBedrockForEmbeds(ec.Bedrock, cfg.Providers.Bedrock)
			if b != nil {
				e, err := embedproviders.NewBedrock(b.Region, b.Profile, b.AccessKeyID, b.SecretAccessKey, b.APIKey)
				if err != nil {
					log.Warn("bedrock embedder failed to initialize; semantic memory may be unavailable",
						zap.Error(err))
				} else {
					register(e)
				}
			}
		case "cohere":
			if ec.Cohere != nil {
				register(embedproviders.NewCohere(ec.Cohere.APIKey))
			} else if cfg.Providers.Cohere != nil {
				register(embedproviders.NewCohere(cfg.Providers.Cohere.APIKey))
			}
		case "google":
			if ec.Google != nil {
				register(embedproviders.NewGoogle(ec.Google.APIKey))
			}
		case "huggingface":
			if ec.HuggingFace != nil {
				register(embedproviders.NewHuggingFace(ec.HuggingFace.APIKey, ec.HuggingFace.BaseURL, ec.HuggingFace.Model))
			}
		case "voyage":
			if ec.Voyage != nil {
				register(embedproviders.NewVoyage(ec.Voyage.APIKey, ec.Voyage.BaseURL))
			} else if cfg.Providers.Voyage != nil {
				register(embedproviders.NewVoyage(cfg.Providers.Voyage.APIKey, cfg.Providers.Voyage.BaseURL))
			}
		case "mistral":
			if ec.Mistral != nil {
				register(embedproviders.NewMistral(ec.Mistral.APIKey, ec.Mistral.BaseURL))
			} else if cfg.Providers.Mistral != nil {
				register(embedproviders.NewMistral(cfg.Providers.Mistral.APIKey, ""))
			}
		case "vertex":
			v := ec.Vertex
			if v == nil && cfg.Providers.Vertex != nil {
				v = cfg.Providers.Vertex
			}
			if v != nil {
				e, err := embedproviders.NewVertex(ctx, v.ProjectID, v.Location, v.Credentials)
				if err == nil {
					register(e)
				}
			}
		}
	}
}

// cliStreamHandler prints tokens to stdout as they arrive.
type cliStreamHandler struct {
	done chan<- error
}

func (h *cliStreamHandler) OnToken(token string) { fmt.Print(token) }
func (h *cliStreamHandler) OnToolCall(name string, _ json.RawMessage) {
	fmt.Printf("\n[calling tool: %s]\n", name)
}
func (h *cliStreamHandler) OnToolResult(name, result string) {
	fmt.Printf("[%s result: %s]\n", name, truncate(result, 100))
}
func (h *cliStreamHandler) OnDone(result *agent.RunResult) {
	fmt.Printf("\n\n[%d iterations, %d tokens]\n", result.Iterations, result.Usage.TotalTokens)
	h.done <- nil
}
func (h *cliStreamHandler) OnError(err error) { h.done <- err }

// runREPL launches the interactive agent REPL.
// It now uses the futuristic bubbletea TUI from cmd/opsintelligence/tui.
func runREPL(ctx context.Context, r *agent.Runner, log *zap.Logger) error {
	// Count providers and skills for the banner
	providerCount := 1 // at least one is configured or we wouldn't be here
	skillCount := 0
	if home, err := os.UserHomeDir(); err == nil {
		if entries, err := os.ReadDir(filepath.Join(home, ".opsintelligence", "skills", "custom")); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					skillCount++
				}
			}
		}
	}

	// Wrap agent.Runner as tui.AgentRunner
	a := &agentRunnerAdapter{runner: r}
	return tui.RunREPL(ctx, a, version, providerCount, skillCount)
}

// agentRunnerAdapter wraps agent.Runner to satisfy tui.AgentRunner.
type agentRunnerAdapter struct {
	runner *agent.Runner
}

func (a *agentRunnerAdapter) SessionID() string { return a.runner.SessionID() }
func (a *agentRunnerAdapter) Run(ctx context.Context, msg string) (*tui.RunResult, error) {
	res, err := a.runner.Run(ctx, memory.Message{
		ID:        uuid.New().String(),
		SessionID: a.runner.SessionID(),
		Role:      memory.RoleUser,
		Content:   msg,
		CreatedAt: time.Now(),
	})
	if err != nil || res == nil {
		return nil, err
	}
	return &tui.RunResult{
		Iterations: res.Iterations,
		Usage:      struct{ TotalTokens int }{TotalTokens: res.Usage.TotalTokens},
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
